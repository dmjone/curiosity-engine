package engine

import (
	"fmt"
	"strings"

	"github.com/dmjone/curiosity-engine/internal/discord"
	"github.com/dmjone/curiosity-engine/internal/store"
)

// Embed accent colours (Discord expects a decimal RGB integer).
const (
	colorEasy   = 0x57F287
	colorMedium = 0xFEE75C
	colorHard   = 0xE67E22
	colorBoss   = 0x9B59B6
	colorInfo   = 0x5865F2
	colorGold   = 0xF1C40F
)

func difficultyColor(d string) int {
	switch d {
	case "easy":
		return colorEasy
	case "hard":
		return colorHard
	case "boss":
		return colorBoss
	default:
		return colorMedium
	}
}

func difficultyBadge(d string) string {
	switch d {
	case "easy":
		return "🟢 Easy"
	case "hard":
		return "🟠 Hard"
	case "boss":
		return "🟣 BOSS"
	default:
		return "🟡 Medium"
	}
}

// ProblemEmbed renders a problem as a rich Discord embed.
func ProblemEmbed(p *store.Problem, subj Subject) discord.Embed {
	title := p.Title
	if p.Difficulty == "boss" {
		title = "👑 FRIDAY BOSS — " + title
	}
	desc := p.Statement
	if len(p.Hints) > 0 {
		desc += "\n\n*Stuck? A hint:* ||" + p.Hints[0] + "||"
	}
	footer := fmt.Sprintf("%s · %s · %s · worth %d pts · tagged %s",
		subj.DisplayName, difficultyBadge(p.Difficulty), p.Date, p.Points, p.CO)
	if p.Difficulty == "boss" {
		footer += " · solve it to unlock hack-night invites"
	}
	return discord.Embed{
		Title:       title,
		Description: desc,
		Color:       difficultyColor(p.Difficulty),
		Footer:      &discord.EmbedFooter{Text: footer},
	}
}

// SolveRow is the action row carrying the Solve button for a problem.
func SolveRow(problemID string) discord.ActionRow {
	return discord.ActionRow{
		Type: discord.ComponentActionRow,
		Components: []discord.Component{{
			Type:     discord.ComponentButton,
			Style:    discord.ButtonStylePrimary,
			Label:    "🧩 Solve this",
			CustomID: "solve|" + problemID,
		}},
	}
}

// ProblemMessage builds the channel message that announces a daily problem.
func ProblemMessage(p *store.Problem, subj Subject) discord.MessagePayload {
	embed := ProblemEmbed(p, subj)
	content := "🧠 **Today's challenge is live.** First to crack it earns ⚡ First Blood."
	if p.Difficulty == "boss" {
		content = "🔥 **BOSS PROBLEM.** Friday is here. Beat it and your name carries weight."
	}
	return discord.MessagePayload{
		Content:    content,
		Embeds:     []discord.Embed{embed},
		Components: []discord.ActionRow{SolveRow(p.ID)},
	}
}

// Badges derives the achievement badges a user has earned. Badges are computed
// from counters, never stored, so the rules can evolve without a migration.
func Badges(u *store.User) []string {
	if u == nil {
		return nil
	}
	var b []string
	add := func(cond bool, badge string) {
		if cond {
			b = append(b, badge)
		}
	}
	add(u.Solves >= 1, "🌱 Initiate")
	add(u.Solves >= 25, "🧠 Scholar")
	add(u.Solves >= 100, "📚 Polymath")
	add(u.LongestStreak >= 3, "🔥 On Fire")
	add(u.LongestStreak >= 7, "⚔️ Week Warrior")
	add(u.LongestStreak >= 30, "🏛️ Unbroken")
	add(u.Points >= 100, "💯 Century")
	add(u.Points >= 500, "👑 Luminary")
	add(u.FirstBloods >= 1, "⚡ First Blood")
	add(u.FirstBloods >= 10, "🩸 Apex Predator")
	add(u.BossSolves >= 1, "🐉 Boss Slayer")
	add(u.BossSolves >= 5, "☄️ Boss Hunter")
	return b
}

// BadgeLine joins badges for compact display.
func BadgeLine(u *store.User) string {
	b := Badges(u)
	if len(b) == 0 {
		return "— no badges yet —"
	}
	return strings.Join(b, "  ")
}
