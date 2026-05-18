package server

import (
	"crypto/rand"
	"encoding/base64"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dmjone/curiosity-engine/internal/engine"
)

// handleWeb serves the public landing page and live leaderboard at the site
// root. It is a read-only, server-rendered view: no JavaScript, no client
// state, so it is fast, accessible, and cheap to serve from a cold start.
//
// The page is designed to stand on its own before any student has scored:
// the leaderboard is just one section of a full landing page, so an empty
// database still renders something that explains the product.
func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.Leaderboard(r.Context(), 25)
	if err != nil {
		// A leaderboard query failure must not blank the page; log it and
		// still render the landing content.
		slog.Error("web: leaderboard query", "err", err)
		users = nil
	}

	nonce := randomNonce()
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'nonce-"+nonce+"'; img-src 'self' data:; "+
			"base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := webData{
		Nonce:   nonce,
		Updated: time.Now().In(istLoc).Format("02 Jan 2006, 15:04 IST"),
		Rows:    make([]webRow, 0, len(users)),
	}
	for _, subj := range engine.Syllabus {
		data.Subjects = append(data.Subjects, subj.DisplayName)
	}
	medals := []string{"🥇", "🥈", "🥉"}
	for i, u := range users {
		medal := ""
		if i < len(medals) {
			medal = medals[i]
		}
		data.Rows = append(data.Rows, webRow{
			Rank:   i + 1,
			Medal:  medal,
			Name:   u.Name,
			Points: u.Points,
			Streak: u.CurrentStreak,
			Solves: u.Solves,
			Badges: strings.Join(engine.Badges(u), " "),
		})
	}
	data.HasRows = len(data.Rows) > 0

	if err := leaderboardTmpl.Execute(w, data); err != nil {
		slog.Error("web: render", "err", err)
	}
}

type webData struct {
	Nonce    string
	Updated  string
	Rows     []webRow
	HasRows  bool
	Subjects []string
}

type webRow struct {
	Rank   int
	Medal  string
	Name   string
	Points int
	Streak int
	Solves int
	Badges string
}

// randomNonce returns a fresh, URL-safe base64 CSP nonce per response.
// URL-safe encoding avoids '+' and '/', which would otherwise be HTML-escaped
// inside the style attribute and break the nonce match.
func randomNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

var leaderboardTmpl = template.Must(template.New("leaderboard").Parse(leaderboardHTML))

const leaderboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CuriosityEngine — turn every CSE subject into an arena</title>
<meta name="description" content="A peer-competition engine for CSE students: a daily syllabus-tagged problem, streaks, badges and a live leaderboard.">
<style nonce="{{.Nonce}}">
  :root { color-scheme: dark; }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: ui-monospace, "Cascadia Code", Menlo, Consolas, monospace;
    background:
      radial-gradient(1100px 560px at 50% -8%, #2c2247 0%, transparent 62%),
      linear-gradient(180deg, #0c0a14 0%, #14111f 100%);
    color: #e9e6f5; min-height: 100vh; padding: 2.6rem 1rem 4rem;
    line-height: 1.5;
  }
  main { max-width: 820px; margin: 0 auto; }
  .hero { text-align: center; margin-bottom: 2.6rem; }
  h1 {
    font-size: clamp(2rem, 6vw, 3.2rem); letter-spacing: -0.03em;
    background: linear-gradient(180deg, #ffe9a8, #f1c40f 55%, #b8860b);
    -webkit-background-clip: text; background-clip: text; color: transparent;
    text-shadow: 0 1px 0 rgba(0,0,0,0.4);
  }
  .tag { color: #c6bee4; margin-top: 0.7rem; font-size: 0.98rem; }
  .sub { color: #8a82a8; margin-top: 0.35rem; font-size: 0.82rem; }
  h2 {
    font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.14em;
    color: #8a82a8; margin: 2.4rem 0 1rem; font-weight: 600;
  }
  .grid { display: grid; gap: 0.7rem; grid-template-columns: repeat(3, 1fr); }
  .card {
    border-radius: 14px; padding: 1.1rem 1rem;
    background: linear-gradient(180deg, #221c33, #181423);
    border: 1px solid #322a48;
    box-shadow: inset 0 1px 0 rgba(255,255,255,0.05), 0 10px 22px rgba(0,0,0,0.4);
  }
  .card .ic { font-size: 1.5rem; }
  .card .ct { font-weight: 700; margin: 0.45rem 0 0.25rem; color: #f1c40f; font-size: 0.92rem; }
  .card .cd { color: #b3abce; font-size: 0.82rem; }
  .pills { display: flex; flex-wrap: wrap; gap: 0.5rem; }
  .pill {
    background: #1b1628; border: 1px solid #322a48; border-radius: 999px;
    padding: 0.4rem 0.85rem; font-size: 0.8rem; color: #d8d2ec;
  }
  .podium { display: flex; gap: 0.7rem; align-items: flex-end; margin: 1rem 0 1.4rem; }
  .podium .seat {
    flex: 1; border-radius: 14px; padding: 1rem 0.6rem; text-align: center;
    background: linear-gradient(180deg, #221c33, #181423);
    border: 1px solid #322a48;
    box-shadow: inset 0 1px 0 rgba(255,255,255,0.05), 0 10px 24px rgba(0,0,0,0.45);
  }
  .podium .first { transform: translateY(-18px); border-color: #f1c40f55; }
  .podium .medal { font-size: 1.9rem; }
  .podium .nm { font-weight: 700; margin: 0.35rem 0 0.15rem; word-break: break-word; }
  .podium .pp { color: #f1c40f; font-size: 1.2rem; }
  .podium .ps { color: #9b93b8; font-size: 0.75rem; }
  table { width: 100%; border-collapse: separate; border-spacing: 0 0.45rem; }
  caption { text-align: left; color: #8a82a8; font-size: 0.78rem; margin-bottom: 0.6rem; }
  th { text-align: left; color: #8a82a8; font-weight: 500; font-size: 0.7rem;
       text-transform: uppercase; letter-spacing: 0.08em; padding: 0 0.8rem; }
  td { background: #1b1628; padding: 0.7rem 0.8rem; }
  tr td:first-child { border-radius: 10px 0 0 10px; }
  tr td:last-child { border-radius: 0 10px 10px 0; }
  .rank { color: #f1c40f; font-weight: 700; width: 3.2rem; }
  .ptcol { color: #ffe9a8; text-align: right; font-variant-numeric: tabular-nums; }
  .badges { color: #8a82a8; font-size: 0.78rem; }
  .panel {
    border-radius: 16px; padding: 1.8rem 1.4rem; text-align: center;
    background: linear-gradient(180deg, #221c33, #181423);
    border: 1px dashed #3c3358;
  }
  .panel .big { font-size: 1.05rem; color: #e9e6f5; font-weight: 700; }
  .panel .small { color: #9b93b8; font-size: 0.84rem; margin-top: 0.5rem; }
  footer { text-align: center; color: #6f6890; font-size: 0.74rem; margin-top: 2.8rem; }
  a { color: #c9a8ff; }
  @media (max-width: 560px) { .grid { grid-template-columns: 1fr; } }
</style>
</head>
<body>
<main>
  <header class="hero">
    <h1>CuriosityEngine</h1>
    <p class="tag">Every CSE subject, turned into a public arena. Solve. Streak. Climb.</p>
    <p class="sub">A daily, syllabus-tagged challenge per subject. You are not doing extra work, you are doing the coursework, gamified.</p>
  </header>

  <h2>How it works</h2>
  <section class="grid">
    <div class="card">
      <div class="ic">🧩</div>
      <div class="ct">A fresh problem</div>
      <div class="cd">Every morning a challenge drops in each subject channel, tagged to an official course outcome.</div>
    </div>
    <div class="card">
      <div class="ic">⚡</div>
      <div class="ct">Solve it</div>
      <div class="cd">Press Solve, submit your answer. Correct answers score instantly. First solver takes First Blood.</div>
    </div>
    <div class="card">
      <div class="ic">🔥</div>
      <div class="ct">Climb</div>
      <div class="cd">Build daily streaks, collect badges, beat the Friday boss, and rise up this board.</div>
    </div>
  </section>

  <h2>Subjects in play</h2>
  <section class="pills">
    {{range .Subjects}}<span class="pill">{{.}}</span>{{end}}
  </section>

  <h2>Leaderboard</h2>
  {{if .HasRows}}
  <section class="podium" aria-label="Top three">
    {{range .Rows}}{{if le .Rank 3}}
    <div class="seat {{if eq .Rank 1}}first{{end}}">
      <div class="medal">{{.Medal}}</div>
      <div class="nm">{{.Name}}</div>
      <div class="pp">{{.Points}} pts</div>
      <div class="ps">🔥 {{.Streak}} streak</div>
    </div>
    {{end}}{{end}}
  </section>
  <table>
    <caption>Updated {{.Updated}}</caption>
    <thead>
      <tr><th scope="col">#</th><th scope="col">Curious mind</th>
          <th scope="col">Solved</th><th scope="col">Badges</th>
          <th scope="col" class="ptcol">Points</th></tr>
    </thead>
    <tbody>
      {{range .Rows}}
      <tr>
        <td class="rank">{{if .Medal}}{{.Medal}}{{else}}{{.Rank}}{{end}}</td>
        <td>{{.Name}}</td>
        <td>{{.Solves}}</td>
        <td class="badges">{{.Badges}}</td>
        <td class="ptcol">{{.Points}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
  {{else}}
  <section class="panel">
    <div class="big">The board is empty. That is an opportunity.</div>
    <div class="small">No one has scored yet. Solve the first problem and your name lights up this page, alone, at the top.</div>
  </section>
  {{end}}

  <footer>
    Runs scale-to-zero on Google Cloud Run. Self-maintaining via Vertex AI research.<br>
    <a href="https://curiosityengine.dmj.one">curiosityengine.dmj.one</a>
  </footer>
</main>
</body>
</html>`
