// Package engine holds the gamification domain logic: the CSE syllabus, the
// daily problem-generation pass, and the rendering of problems and profiles.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dmjone/curiosity-engine/internal/discord"
	"github.com/dmjone/curiosity-engine/internal/store"
	"github.com/dmjone/curiosity-engine/internal/vertex"
)

// Engine runs the daily content refresh.
type Engine struct {
	st *store.Store
	vx *vertex.Client
}

// New constructs an Engine.
func New(st *store.Store, vx *vertex.Client) *Engine {
	return &Engine{st: st, vx: vx}
}

// difficultyRotation cycles weekday difficulty so the feed stays varied.
var difficultyRotation = []string{"easy", "medium", "medium", "hard"}

// problemSpec is the JSON contract we ask Gemini to return.
type problemSpec struct {
	Title       string   `json:"title"`
	Statement   string   `json:"statement"`
	Answer      string   `json:"answer"`
	Hints       []string `json:"hints"`
	Explanation string   `json:"explanation"`
}

// DailyResult summarises a daily content pass.
type DailyResult struct {
	Posted        int
	Skipped       int
	StreaksBroken int
	Errors        []string
}

// RunDaily generates and posts one problem per registered channel, decays
// stale streaks, and re-syncs slash commands. It is safe to call more than
// once a day: a topic that already has a problem for `today` is skipped.
func (e *Engine) RunDaily(ctx context.Context, rest *discord.REST, appID, today, yesterday string, dayOfYear int, isBoss bool) (*DailyResult, error) {
	res := &DailyResult{}

	// Self-heal: ensure the slash commands are registered. Idempotent PUT.
	if err := rest.RegisterCommands(ctx, appID, CommandDefs()); err != nil {
		res.Errors = append(res.Errors, "command sync: "+err.Error())
		slog.Error("daily: command sync failed", "err", err)
	}

	broken, err := e.st.DecayStreaks(ctx, today, yesterday)
	if err != nil {
		res.Errors = append(res.Errors, "streak decay: "+err.Error())
		slog.Error("daily: streak decay failed", "err", err)
	}
	res.StreaksBroken = broken

	channels, err := e.st.Channels(ctx)
	if err != nil {
		return res, fmt.Errorf("load channels: %w", err)
	}

	for topic, channelID := range channels {
		subj, ok := SubjectByTopic(topic)
		if !ok {
			continue
		}

		// Idempotency: never post a second problem for the same topic/day.
		if existing, _ := e.st.GetProblem(ctx, today, topic); existing != nil {
			res.Skipped++
			continue
		}

		difficulty := difficultyRotation[dayOfYear%len(difficultyRotation)]
		if isBoss {
			difficulty = "boss"
		}
		outcome := subj.Outcomes[dayOfYear%len(subj.Outcomes)]

		prob, err := e.generateProblem(ctx, subj, outcome, difficulty, today)
		if err != nil {
			res.Errors = append(res.Errors, topic+": "+err.Error())
			slog.Error("daily: problem generation failed", "topic", topic, "err", err)
			continue
		}
		if err := e.st.SaveProblem(ctx, prob); err != nil {
			res.Errors = append(res.Errors, topic+" save: "+err.Error())
			continue
		}
		if err := rest.PostMessage(ctx, channelID, ProblemMessage(prob, subj)); err != nil {
			res.Errors = append(res.Errors, topic+" post: "+err.Error())
			slog.Error("daily: post failed", "topic", topic, "err", err)
			continue
		}
		res.Posted++
		slog.Info("daily: problem posted", "topic", topic, "difficulty", difficulty)
	}
	return res, nil
}

func difficultyPoints(d string) int {
	switch d {
	case "easy":
		return store.PointsEasy
	case "hard":
		return store.PointsHard
	case "boss":
		return store.PointsBoss
	default:
		return store.PointsMedium
	}
}

func (e *Engine) generateProblem(ctx context.Context, subj Subject, outcome Outcome, difficulty, today string) (*store.Problem, error) {
	prompt := buildPrompt(subj, outcome, difficulty)

	spec, err := e.researchSpec(ctx, prompt)
	if err != nil {
		// One retry with an explicit reformat instruction.
		spec, err = e.researchSpec(ctx, prompt+"\n\nReturn ONLY the JSON object. No prose, no markdown fences.")
		if err != nil {
			return nil, err
		}
	}
	if spec.Title == "" || spec.Statement == "" || spec.Answer == "" {
		return nil, fmt.Errorf("model returned an incomplete problem")
	}

	return &store.Problem{
		Date:       today,
		Topic:      subj.Topic,
		Title:      strings.TrimSpace(spec.Title),
		Statement:  strings.TrimSpace(spec.Statement),
		Difficulty: difficulty,
		Answer:     store.Normalize(spec.Answer),
		Hints:      spec.Hints,
		CO:         outcome.CO,
		Points:     difficultyPoints(difficulty),
	}, nil
}

func (e *Engine) researchSpec(ctx context.Context, prompt string) (*problemSpec, error) {
	raw, err := e.vx.Research(ctx, prompt)
	if err != nil {
		return nil, err
	}
	js := extractJSON(raw)
	if js == "" {
		return nil, fmt.Errorf("no JSON object in model response")
	}
	var spec problemSpec
	if err := json.Unmarshal([]byte(js), &spec); err != nil {
		return nil, fmt.Errorf("parse problem JSON: %w", err)
	}
	return &spec, nil
}

func buildPrompt(subj Subject, outcome Outcome, difficulty string) string {
	window := "5-15 minutes"
	if difficulty == "hard" {
		window = "15-30 minutes"
	}
	if difficulty == "boss" {
		window = "30-60 minutes and noticeably harder than a normal day"
	}
	return fmt.Sprintf(`You are the problem-setter for a university CSE peer-competition bot.

Research current, real, well-regarded practice material (CodeForces, Project Euler,
classic textbook exercises, university problem sets) and design ONE original problem.

Subject: %s
Concept to assess: %s (course outcome %s)
Difficulty: %s — solvable by a diligent undergraduate in about %s.

Hard requirements:
- The problem must be fully self-contained: all data inline, no links, no images.
- It must have exactly ONE deterministic answer: a number, or a single short word
  or token (no sentences). The answer must be objectively checkable.
- Keep the statement under 1200 characters.
- Provide exactly 3 hints, increasing in helpfulness.

Return ONLY a JSON object, no markdown, with this exact shape:
{"title": "...", "statement": "...", "answer": "...", "hints": ["...","...","..."], "explanation": "..."}`,
		subj.DisplayName, outcome.Concept, outcome.CO, difficulty, window)
}

// extractJSON returns the first balanced {...} object found in s.
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// skip
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// CommandDefs returns the global slash commands CuriosityEngine registers.
func CommandDefs() []discord.AppCommand {
	topicChoices := make([]discord.AppCommandChoice, 0, len(Syllabus))
	for _, s := range Syllabus {
		topicChoices = append(topicChoices, discord.AppCommandChoice{
			Name: s.DisplayName, Value: s.Topic,
		})
	}
	return []discord.AppCommand{
		{Name: "problem", Description: "Show this channel's challenge for today"},
		{Name: "leaderboard", Description: "See the top curious minds"},
		{Name: "streak", Description: "Your streak, points and badges"},
		{
			Name:        "ce-setup",
			Description: "Admin: bind this channel to a CSE subject",
			Options: []discord.AppCommandOption{{
				Type:        discord.OptionTypeString,
				Name:        "topic",
				Description: "Subject to post here every day",
				Required:    true,
				Choices:     topicChoices,
			}},
		},
		{
			Name:        "ce-admin",
			Description: "Admin: inspect or maintain the engine",
			Options: []discord.AppCommandOption{{
				Type:        discord.OptionTypeString,
				Name:        "action",
				Description: "What to do",
				Required:    true,
				Choices: []discord.AppCommandChoice{
					{Name: "status", Value: "status"},
					{Name: "sync-commands", Value: "sync"},
				},
			}},
		},
	}
}
