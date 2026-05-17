package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/dmjone/curiosity-engine/internal/discord"
	"github.com/dmjone/curiosity-engine/internal/engine"
	"github.com/dmjone/curiosity-engine/internal/store"
)

// handleInteractions is the Discord HTTP-interaction webhook. It must be
// publicly reachable so Discord can call it, so every request is gated by an
// Ed25519 signature check before anything else happens. All replies are
// returned inline (synchronously) and touch only Firestore, which keeps the
// handler well inside Discord's 3-second response budget even on a cold start.
func (s *Server) handleInteractions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	dc, err := s.discordCfg(r.Context())
	if err != nil {
		slog.Error("interactions: discord config", "err", err)
		http.Error(w, "service not configured", http.StatusServiceUnavailable)
		return
	}

	sig := r.Header.Get("X-Signature-Ed25519")
	ts := r.Header.Get("X-Signature-Timestamp")
	if !discord.Verify(dc.PublicKey, sig, ts, body) {
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}

	var it discord.Interaction
	if err := json.Unmarshal(body, &it); err != nil {
		http.Error(w, "bad interaction", http.StatusBadRequest)
		return
	}

	resp := s.routeInteraction(r.Context(), dc, &it)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) routeInteraction(ctx context.Context, dc *DiscordCfg, it *discord.Interaction) *discord.Response {
	switch it.Type {
	case discord.InteractionPing:
		return &discord.Response{Type: discord.ResponsePong}
	case discord.InteractionCommand:
		return s.handleCommand(ctx, dc, it)
	case discord.InteractionMessageComponent:
		return s.handleComponent(it)
	case discord.InteractionModalSubmit:
		return s.handleModal(ctx, it)
	default:
		return ephemeral("That interaction type is not supported.")
	}
}

func (s *Server) handleCommand(ctx context.Context, dc *DiscordCfg, it *discord.Interaction) *discord.Response {
	if it.Data == nil {
		return ephemeral("Empty command.")
	}
	switch it.Data.Name {
	case "leaderboard":
		return s.cmdLeaderboard(ctx)
	case "streak":
		return s.cmdStreak(ctx, it)
	case "problem":
		return s.cmdProblem(ctx, it)
	case "ce-setup":
		return s.cmdSetup(ctx, dc, it)
	case "ce-admin":
		return s.cmdAdmin(ctx, dc, it)
	default:
		return ephemeral("Unknown command.")
	}
}

func (s *Server) cmdLeaderboard(ctx context.Context) *discord.Response {
	users, err := s.st.Leaderboard(ctx, 15)
	if err != nil {
		slog.Error("leaderboard query", "err", err)
		return ephemeral("Could not load the leaderboard right now.")
	}
	return &discord.Response{
		Type: discord.ResponseMessage,
		Data: &discord.ResponseData{Embeds: []discord.Embed{leaderboardEmbed(users)}},
	}
}

func (s *Server) cmdStreak(ctx context.Context, it *discord.Interaction) *discord.Response {
	u, err := s.st.GetUser(ctx, it.ActorID())
	if err != nil {
		slog.Error("streak query", "err", err)
		return ephemeral("Could not load your profile right now.")
	}
	if u == nil {
		return ephemeral("You have not solved anything yet. Run `/problem` and get on the board!")
	}
	embed := discord.Embed{
		Title: "📈 " + it.ActorName() + "'s curiosity profile",
		Color: 0x5865F2,
		Fields: []discord.EmbedField{
			{Name: "Points", Value: fmt.Sprintf("**%d**", u.Points), Inline: true},
			{Name: "Current streak", Value: fmt.Sprintf("🔥 %d", u.CurrentStreak), Inline: true},
			{Name: "Longest streak", Value: fmt.Sprintf("🏔️ %d", u.LongestStreak), Inline: true},
			{Name: "Problems solved", Value: fmt.Sprintf("%d", u.Solves), Inline: true},
			{Name: "Boss kills", Value: fmt.Sprintf("🐉 %d", u.BossSolves), Inline: true},
			{Name: "First bloods", Value: fmt.Sprintf("⚡ %d", u.FirstBloods), Inline: true},
			{Name: "Badges", Value: engine.BadgeLine(u)},
		},
	}
	return &discord.Response{
		Type: discord.ResponseMessage,
		Data: &discord.ResponseData{Embeds: []discord.Embed{embed}, Flags: discord.FlagEphemeral},
	}
}

func (s *Server) cmdProblem(ctx context.Context, it *discord.Interaction) *discord.Response {
	topic, ok := s.topicForChannel(ctx, it.ChannelID)
	if !ok {
		return ephemeral("This channel is not bound to a subject yet. An admin can run `/ce-setup`.")
	}
	subj, _ := engine.SubjectByTopic(topic)
	today, _, _, _ := istDates()
	p, err := s.st.GetProblem(ctx, today, topic)
	if err != nil {
		slog.Error("problem query", "err", err)
		return ephemeral("Could not load today's problem.")
	}
	if p == nil {
		return ephemeral("No problem has been posted here today yet. It lands every morning, IST.")
	}
	return &discord.Response{
		Type: discord.ResponseMessage,
		Data: &discord.ResponseData{
			Embeds:     []discord.Embed{engine.ProblemEmbed(p, subj)},
			Components: []discord.ActionRow{engine.SolveRow(p.ID)},
			Flags:      discord.FlagEphemeral,
		},
	}
}

func (s *Server) cmdSetup(ctx context.Context, dc *DiscordCfg, it *discord.Interaction) *discord.Response {
	if !s.isAdmin(dc, it) {
		return ephemeral("Only the server admin can bind channels.")
	}
	topic := it.Data.OptString("topic")
	subj, ok := engine.SubjectByTopic(topic)
	if !ok {
		return ephemeral("Unknown subject.")
	}
	if err := s.st.SetChannel(ctx, topic, it.ChannelID); err != nil {
		slog.Error("set channel", "err", err)
		return ephemeral("Could not save that binding.")
	}
	return ephemeral(fmt.Sprintf("✅ This channel now hosts **%s**. The next daily run will post here.", subj.DisplayName))
}

func (s *Server) cmdAdmin(ctx context.Context, dc *DiscordCfg, it *discord.Interaction) *discord.Response {
	if !s.isAdmin(dc, it) {
		return ephemeral("Admin only.")
	}
	switch it.Data.OptString("action") {
	case "status":
		users, _ := s.st.CountUsers(ctx)
		channels, _ := s.st.Channels(ctx)
		state, _ := s.st.GetState(ctx)
		last := "never"
		if state != nil && state.LastRunDate != "" {
			last = state.LastRunDate
		}
		return ephemeral(fmt.Sprintf(
			"🛠️ **CuriosityEngine status**\nParticipants: %d\nBound channels: %d\nLast daily run: %s",
			users, len(channels), last))
	case "sync":
		rest := discord.NewREST(dc.BotToken)
		if err := rest.RegisterCommands(ctx, dc.AppID, engine.CommandDefs()); err != nil {
			slog.Error("command sync", "err", err)
			return ephemeral("Command sync failed: " + err.Error())
		}
		return ephemeral("✅ Slash commands re-synced.")
	default:
		return ephemeral("Unknown action.")
	}
}

// handleComponent turns the "Solve" button into a modal prompting for the
// answer. The modal is the only place an answer is collected, so there is no
// free-form message for the bot to read; channel-chat moderation is left to
// Discord's built-in AutoMod, which needs no always-on bot.
func (s *Server) handleComponent(it *discord.Interaction) *discord.Response {
	if it.Data == nil || !strings.HasPrefix(it.Data.CustomID, "solve|") {
		return ephemeral("Unknown component.")
	}
	problemID := strings.TrimPrefix(it.Data.CustomID, "solve|")
	return &discord.Response{
		Type: discord.ResponseModal,
		Data: &discord.ResponseData{
			CustomID: "answer|" + problemID,
			Title:    "Submit your answer",
			Components: []discord.ActionRow{{
				Type: discord.ComponentActionRow,
				Components: []discord.Component{{
					Type:        discord.ComponentTextInput,
					CustomID:    "answer",
					Style:       discord.TextInputShort,
					Label:       "Your answer",
					Placeholder: "A number or a short token",
					MinLength:   1,
					MaxLength:   200,
					Required:    true,
				}},
			}},
		},
	}
}

func (s *Server) handleModal(ctx context.Context, it *discord.Interaction) *discord.Response {
	if it.Data == nil || !strings.HasPrefix(it.Data.CustomID, "answer|") {
		return ephemeral("Unknown submission.")
	}
	problemID := strings.TrimPrefix(it.Data.CustomID, "answer|")
	answer := it.Data.ModalValue()
	today, yesterday, _, _ := istDates()

	res, err := s.st.Solve(ctx, problemID, it.ActorID(), it.ActorName(), answer, today, yesterday)
	if err != nil {
		slog.Error("solve", "err", err, "problem", problemID)
		return ephemeral("Something went wrong checking that. Try again in a moment.")
	}
	switch {
	case res.NoProblem:
		return ephemeral("That problem is no longer available.")
	case res.AlreadySolved:
		return ephemeral("You have already cracked this one. Save some glory for the others!")
	case !res.Correct:
		return ephemeral("❌ Not quite. Look again, then press **Solve** to retry.")
	default:
		msg := fmt.Sprintf("✅ **Correct!** +%d points.\nPoints: **%d**  ·  Streak: 🔥 **%d**",
			res.PointsAwarded, res.NewPoints, res.NewStreak)
		if res.FirstBlood {
			msg += "\n⚡ **First Blood** — you solved it before anyone else."
		}
		return ephemeral(msg)
	}
}

// topicForChannel reverse-maps a Discord channel id to its bound subject.
func (s *Server) topicForChannel(ctx context.Context, channelID string) (string, bool) {
	channels, err := s.st.Channels(ctx)
	if err != nil {
		slog.Error("channels lookup", "err", err)
		return "", false
	}
	for topic, ch := range channels {
		if ch == channelID {
			return topic, true
		}
	}
	return "", false
}

func (s *Server) isAdmin(dc *DiscordCfg, it *discord.Interaction) bool {
	return dc.AdminUserID != "" && it.ActorID() == dc.AdminUserID
}

func ephemeral(content string) *discord.Response {
	return &discord.Response{
		Type: discord.ResponseMessage,
		Data: &discord.ResponseData{Content: content, Flags: discord.FlagEphemeral},
	}
}

func leaderboardEmbed(users []*store.User) discord.Embed {
	if len(users) == 0 {
		return discord.Embed{
			Title:       "🏆 CuriosityEngine Leaderboard",
			Description: "No scores yet. Solve today's problem and plant your flag.",
			Color:       0xF1C40F,
		}
	}
	medals := []string{"🥇", "🥈", "🥉"}
	var b strings.Builder
	for i, u := range users {
		rank := fmt.Sprintf("`#%2d`", i+1)
		if i < len(medals) {
			rank = medals[i]
		}
		fmt.Fprintf(&b, "%s  **%s** — %d pts  ·  🔥 %d\n", rank, u.Name, u.Points, u.CurrentStreak)
	}
	return discord.Embed{
		Title:       "🏆 CuriosityEngine Leaderboard",
		Description: b.String(),
		Color:       0xF1C40F,
		Footer:      &discord.EmbedFooter{Text: "Full board: https://curiosityengine.dmj.one"},
	}
}
