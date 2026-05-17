package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"google.golang.org/api/idtoken"

	"github.com/dmjone/curiosity-engine/internal/discord"
	"github.com/dmjone/curiosity-engine/internal/selfupdate"
)

// handleCron is the once-a-day self-update entrypoint, invoked by Cloud
// Scheduler. The service is otherwise fully idle: this request is the only
// thing that wakes it on a quiet day, and the work below is exactly the
// "self check and update if necessary" pass.
//
// The endpoint is reachable on the public URL (Cloud Run allows unauthenticated
// ingress so Discord can reach /interactions), so it is protected at the
// application layer: only a Google-signed OIDC token minted for the dedicated
// scheduler service account is accepted.
func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := s.verifyCron(ctx, r); err != nil {
		slog.Warn("cron: rejected unauthorized call", "err", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	today, yesterday, dayOfYear, isFriday := istDates()
	force := r.URL.Query().Get("force") == "1"

	state, err := s.st.GetState(ctx)
	if err != nil {
		slog.Error("cron: read state", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "state read failed"})
		return
	}

	// At most one update per day. If today's run already happened, this is a
	// no-op: the data is already fresh, exactly as intended.
	if state.LastRunDate == today && !force {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "fresh",
			"lastRunDate": today,
			"note":        "already updated today; nothing to do",
		})
		return
	}

	summary := map[string]any{"date": today}

	dc, err := s.discordCfg(ctx)
	if err != nil {
		// Without Discord credentials we cannot post; report and stop rather
		// than half-running. The scheduler will simply try again tomorrow.
		slog.Error("cron: discord config", "err", err)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "skipped",
			"reason": "curiosity-discord secret is not set yet",
		})
		return
	}
	rest := discord.NewREST(dc.BotToken)

	// Step 1: content self-update. Research and post today's problems.
	dr, err := s.eng.RunDaily(ctx, rest, dc.AppID, today, yesterday, dayOfYear, isFriday)
	if err != nil {
		slog.Error("cron: daily content failed", "err", err)
		summary["content_error"] = err.Error()
	}
	if dr != nil {
		summary["problems_posted"] = dr.Posted
		summary["problems_skipped"] = dr.Skipped
		summary["streaks_broken"] = dr.StreaksBroken
		if len(dr.Errors) > 0 {
			summary["content_warnings"] = dr.Errors
		}
	}

	// Step 2: source self-update. Open at most one enterprise-improvement PR.
	summary["self_update"] = s.runSelfUpdate(ctx, today)

	// Step 3: record the run so any further call today is a clean no-op.
	state.LastRunDate = today
	state.LastRunAt = time.Now()
	state.Runs++
	if err := s.st.SaveState(ctx, state); err != nil {
		slog.Error("cron: save state", "err", err)
		summary["state_error"] = err.Error()
	}

	summary["status"] = "updated"
	slog.Info("cron: daily run complete", "date", today)
	writeJSON(w, http.StatusOK, summary)
}

// verifyCron authenticates a scheduler call via its OIDC identity token.
func (s *Server) verifyCron(ctx context.Context, r *http.Request) error {
	if s.cfg.ExpectedAudience == "" || s.cfg.SchedulerSAEmail == "" {
		return fmt.Errorf("cron authentication is not configured")
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return fmt.Errorf("missing bearer token")
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))

	payload, err := idtoken.Validate(ctx, token, s.cfg.ExpectedAudience)
	if err != nil {
		return fmt.Errorf("oidc validation: %w", err)
	}
	email, _ := payload.Claims["email"].(string)
	if email != s.cfg.SchedulerSAEmail {
		return fmt.Errorf("token identity %q is not the scheduler account", email)
	}
	if verified, _ := payload.Claims["email_verified"].(bool); !verified {
		return fmt.Errorf("token identity is not verified")
	}
	return nil
}

// runSelfUpdate runs the GitHub self-maintenance agent and returns a JSON-safe
// summary. A failure here is logged but never fails the daily run: keeping the
// problem feed alive matters more than a missed maintenance PR.
func (s *Server) runSelfUpdate(ctx context.Context, today string) any {
	token, err := s.sm.Get(ctx, secretGitHub)
	if err != nil || strings.TrimSpace(token) == "" {
		slog.Warn("cron: self-update skipped, no github secret", "err", err)
		return map[string]any{"status": "skipped", "reason": "curiosity-github secret is not set yet"}
	}

	agent := selfupdate.New(selfupdate.Config{
		Token:         strings.TrimSpace(token),
		Owner:         s.cfg.GitHubOwner,
		Repo:          s.cfg.GitHubRepo,
		DefaultBranch: s.cfg.GitHubBranch,
	}, s.vx)

	res, err := agent.Run(ctx, today)
	if err != nil {
		slog.Error("cron: self-update failed", "err", err)
		return map[string]any{"status": "error", "error": err.Error()}
	}
	if res.Skipped {
		return map[string]any{"status": "no-change", "summary": res.Summary}
	}
	slog.Info("cron: self-update PR opened", "pr", res.PRURL, "branch", res.Branch)
	return map[string]any{
		"status":  "pr-opened",
		"pr":      res.PRURL,
		"branch":  res.Branch,
		"files":   res.FilesChanged,
		"summary": res.Summary,
	}
}
