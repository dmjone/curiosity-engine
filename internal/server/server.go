// Package server wires the three CuriosityEngine surfaces onto one HTTP mux:
// the Discord interaction webhook, the daily self-update cron, and the public
// leaderboard. It runs as a single Cloud Run service so the whole product
// lives in one process and one deployment, with nothing always-on.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/dmjone/curiosity-engine/internal/config"
	"github.com/dmjone/curiosity-engine/internal/engine"
	"github.com/dmjone/curiosity-engine/internal/secrets"
	"github.com/dmjone/curiosity-engine/internal/store"
	"github.com/dmjone/curiosity-engine/internal/vertex"
)

// Secret names in Secret Manager. The runtime service account is granted
// access to exactly these two and nothing else.
const (
	secretDiscord = "curiosity-discord"
	secretGitHub  = "curiosity-github"
)

// DiscordCfg is the JSON payload of the curiosity-discord secret. Keeping all
// Discord credentials in one secret means one thing for the operator to fill
// and one IAM grant to reason about.
type DiscordCfg struct {
	AppID       string `json:"app_id"`
	PublicKey   string `json:"public_key"`
	BotToken    string `json:"bot_token"`
	AdminUserID string `json:"admin_user_id"`
}

// Server holds the shared dependencies for all handlers.
type Server struct {
	cfg *config.Config
	st  *store.Store
	vx  *vertex.Client
	sm  *secrets.Manager
	eng *engine.Engine

	dcMu sync.Mutex
	dc   *DiscordCfg // cached per instance
}

// New constructs a Server.
func New(cfg *config.Config, st *store.Store, vx *vertex.Client, sm *secrets.Manager) *Server {
	return &Server{
		cfg: cfg,
		st:  st,
		vx:  vx,
		sm:  sm,
		eng: engine.New(st, vx),
	}
}

// Handler returns the fully routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /interactions", s.handleInteractions)
	mux.HandleFunc("POST /cron/daily", s.handleCron)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /{$}", s.handleWeb)
	return withSecurityHeaders(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// discordCfg lazily loads and caches the Discord credentials secret.
func (s *Server) discordCfg(ctx context.Context) (*DiscordCfg, error) {
	s.dcMu.Lock()
	defer s.dcMu.Unlock()
	if s.dc != nil {
		return s.dc, nil
	}
	raw, err := s.sm.Get(ctx, secretDiscord)
	if err != nil {
		return nil, err
	}
	var dc DiscordCfg
	if err := json.Unmarshal([]byte(raw), &dc); err != nil {
		return nil, fmt.Errorf("parse %s secret: %w", secretDiscord, err)
	}
	if dc.AppID == "" || dc.PublicKey == "" {
		return nil, fmt.Errorf("%s secret missing app_id or public_key", secretDiscord)
	}
	s.dc = &dc
	return s.dc, nil
}

// writeJSON serialises v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// withSecurityHeaders applies hardening headers to every response. The actual
// content rules (CSP) are tightened further per-handler where needed.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// istLoc is the Asia/Kolkata location; the daily cadence is anchored to IST
// because the audience is an Indian university. The tzdata is embedded in the
// binary (see the blank import in main), so this never fails on distroless.
var istLoc = mustIST()

func mustIST() *time.Location {
	l, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		return time.FixedZone("IST", 5*3600+30*60)
	}
	return l
}

// istDates returns the IST date strings and calendar facts the daily logic
// needs. Anchoring everything to one timezone keeps streaks unambiguous.
func istDates() (today, yesterday string, dayOfYear int, isFriday bool) {
	n := time.Now().In(istLoc)
	today = n.Format("2006-01-02")
	yesterday = n.AddDate(0, 0, -1).Format("2006-01-02")
	return today, yesterday, n.YearDay(), n.Weekday() == time.Friday
}
