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

// handleWeb serves the public, server-rendered leaderboard at the site root.
// It is a read-only view: no JavaScript, no client state, so the page is fast,
// accessible, and cheap to serve straight out of a cold start.
func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.Leaderboard(r.Context(), 25)
	if err != nil {
		slog.Error("web: leaderboard query", "err", err)
		http.Error(w, "leaderboard unavailable", http.StatusInternalServerError)
		return
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

	if err := leaderboardTmpl.Execute(w, data); err != nil {
		slog.Error("web: render", "err", err)
	}
}

type webData struct {
	Nonce   string
	Updated string
	Rows    []webRow
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

// randomNonce returns a fresh base64 CSP nonce per response.
func randomNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}

var leaderboardTmpl = template.Must(template.New("leaderboard").Parse(leaderboardHTML))

const leaderboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CuriosityEngine — Leaderboard</title>
<meta name="description" content="The live peer-competition leaderboard for CSE students.">
<style nonce="{{.Nonce}}">
  :root { color-scheme: dark; }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: ui-monospace, "Cascadia Code", Menlo, Consolas, monospace;
    background:
      radial-gradient(1200px 600px at 50% -10%, #2a2140 0%, transparent 60%),
      linear-gradient(180deg, #0c0a14 0%, #14111f 100%);
    color: #e9e6f5; min-height: 100vh; padding: 2.5rem 1rem 4rem;
  }
  main { max-width: 760px; margin: 0 auto; }
  header { text-align: center; margin-bottom: 2.4rem; }
  h1 {
    font-size: clamp(1.8rem, 5vw, 2.8rem); letter-spacing: -0.02em;
    background: linear-gradient(180deg, #ffe9a8, #f1c40f 55%, #b8860b);
    -webkit-background-clip: text; background-clip: text; color: transparent;
    text-shadow: 0 1px 0 rgba(0,0,0,0.4);
  }
  .tag { color: #9b93b8; margin-top: 0.5rem; font-size: 0.85rem; }
  .podium { display: flex; gap: 0.7rem; align-items: flex-end; margin: 2rem 0 1.6rem; }
  .podium .seat {
    flex: 1; border-radius: 14px; padding: 1rem 0.6rem; text-align: center;
    background: linear-gradient(180deg, #221c33, #181423);
    border: 1px solid #322a48;
    box-shadow: inset 0 1px 0 rgba(255,255,255,0.05), 0 10px 24px rgba(0,0,0,0.45);
  }
  .podium .first  { transform: translateY(-18px); border-color: #f1c40f55; }
  .podium .medal { font-size: 1.9rem; }
  .podium .nm { font-weight: 700; margin: 0.35rem 0 0.15rem; word-break: break-word; }
  .podium .pts { color: #f1c40f; font-size: 1.2rem; }
  .podium .sub { color: #9b93b8; font-size: 0.75rem; }
  table { width: 100%; border-collapse: separate; border-spacing: 0 0.45rem; }
  caption { text-align: left; color: #9b93b8; font-size: 0.8rem; margin-bottom: 0.6rem; }
  th { text-align: left; color: #8a82a8; font-weight: 500; font-size: 0.72rem;
       text-transform: uppercase; letter-spacing: 0.08em; padding: 0 0.8rem; }
  td { background: #1b1628; padding: 0.7rem 0.8rem; }
  tr td:first-child { border-radius: 10px 0 0 10px; }
  tr td:last-child  { border-radius: 0 10px 10px 0; }
  .rank { color: #f1c40f; font-weight: 700; width: 3.2rem; }
  .pts { color: #ffe9a8; text-align: right; font-variant-numeric: tabular-nums; }
  .badges { color: #8a82a8; font-size: 0.78rem; }
  .empty { text-align: center; color: #9b93b8; padding: 2.5rem 1rem; }
  footer { text-align: center; color: #6f6890; font-size: 0.74rem; margin-top: 2.4rem; }
  a { color: #c9a8ff; }
</style>
</head>
<body>
<main>
  <header>
    <h1>CuriosityEngine</h1>
    <p class="tag">Every CSE subject, turned into a public arena. Solve. Streak. Climb.</p>
  </header>

  {{if .Rows}}
  <section class="podium" aria-label="Top three">
    {{range .Rows}}{{if le .Rank 3}}
    <div class="seat {{if eq .Rank 1}}first{{end}}">
      <div class="medal">{{.Medal}}</div>
      <div class="nm">{{.Name}}</div>
      <div class="pts">{{.Points}} pts</div>
      <div class="sub">🔥 {{.Streak}} streak</div>
    </div>
    {{end}}{{end}}
  </section>

  <table>
    <caption>Updated {{.Updated}}</caption>
    <thead>
      <tr><th scope="col">#</th><th scope="col">Curious mind</th>
          <th scope="col">Solved</th><th scope="col">Badges</th>
          <th scope="col" class="pts">Points</th></tr>
    </thead>
    <tbody>
      {{range .Rows}}
      <tr>
        <td class="rank">{{if .Medal}}{{.Medal}}{{else}}{{.Rank}}{{end}}</td>
        <td>{{.Name}}</td>
        <td>{{.Solves}}</td>
        <td class="badges">{{.Badges}}</td>
        <td class="pts">{{.Points}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
  {{else}}
  <p class="empty">No scores yet. The first solved problem lights up this board.</p>
  {{end}}

  <footer>
    Runs scale-to-zero on Google Cloud Run. Self-maintaining via Vertex AI.<br>
    <a href="https://curiosityengine.dmj.one">curiosityengine.dmj.one</a>
  </footer>
</main>
</body>
</html>`
