package selfupdate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/dmjone/curiosity-engine/internal/vertex"
)

// Config carries the GitHub identity the agent needs to open pull requests.
// The Token must be a fine-grained PAT with Contents (write) and Pull requests
// (write) on the target repository only; it must have NO access to anything else.
type Config struct {
	Token         string // GitHub fine-grained PAT (contents+PR write on one repo)
	Owner         string // e.g. "dmjone"
	Repo          string // e.g. "curiosity-engine"
	DefaultBranch string // e.g. "main"
}

// Result describes the outcome of one Agent.Run call.
type Result struct {
	Skipped      bool   // true when no change was warranted today
	Branch       string // branch created, if any
	PRURL        string // html URL of the opened PR, if any
	PRNumber     int
	FilesChanged []string
	Summary      string // one-paragraph human-readable summary (always set)
}

// Agent is the daily self-maintenance agent. It researches current enterprise
// engineering practices and opens a PR proposing improvements to the repo.
// It never pushes to the default branch directly.
type Agent struct {
	cfg Config
	gh  *ghClient
	vx  *vertex.Client
}

// New constructs an Agent. vx is the Vertex/Gemini client used for grounded
// research; gh is the GitHub client built from cfg.
func New(cfg Config, vx *vertex.Client) *Agent {
	return &Agent{
		cfg: cfg,
		gh:  newGHClient(cfg.Token, cfg.Owner, cfg.Repo),
		vx:  vx,
	}
}

// guardedPrefixes lists path prefixes whose files the agent must never modify.
// Touching these would let a model-generated response take over the agent's own
// guardrails, CI/CD, or container definitions — any of which could be
// catastrophic. The list is intentionally hard-coded, not configurable.
var guardedPrefixes = []string{
	"internal/selfupdate/",
	".github/",
	"deploy/",
}

// guardedExact is the set of top-level files the agent must never overwrite.
var guardedExact = map[string]bool{
	"cloudbuild.yaml": true,
	"Dockerfile":      true,
	".dockerignore":   true,
}

// contextCap is the soft byte limit on the repo context we send to Gemini.
// Staying under 60 KB keeps the prompt within the grounded-search token budget.
const contextCap = 60 * 1024

// branchRef returns the full git ref string for a branch name.
func branchRef(branch string) string { return "refs/heads/" + branch }

// Run performs one daily self-update pass. today must be in "2006-01-02" format.
//
// The call is safe to repeat: if the branch auto/enterprise-<today> already
// exists (the run already happened today), it returns immediately with
// Skipped=true and no error.
func (a *Agent) Run(ctx context.Context, today string) (*Result, error) {
	branchName := "auto/enterprise-" + today
	ref := branchRef(branchName)

	// --- Step 1: resolve the default branch HEAD ---
	slog.Info("selfupdate: resolving default branch HEAD", "branch", a.cfg.DefaultBranch)
	headRef, err := a.gh.getRef(ctx, branchRef(a.cfg.DefaultBranch))
	if err != nil {
		return nil, fmt.Errorf("get HEAD ref: %w", err)
	}
	headSHA := headRef.Object.SHA

	headCommit, err := a.gh.getCommit(ctx, headSHA)
	if err != nil {
		return nil, fmt.Errorf("get HEAD commit: %w", err)
	}
	treeSHA := headCommit.Tree.SHA

	// --- Step 2: check whether today's branch already exists (idempotency) ---
	// We attempt to get it; a 404 means we haven't run yet today.
	existing, err := a.gh.getRef(ctx, ref)
	if err == nil && existing.Object.SHA != "" {
		slog.Info("selfupdate: branch already exists, skipping", "branch", branchName)
		return &Result{Skipped: true, Summary: "already ran today"}, nil
	}
	// Any error other than "not found" is unexpected; treat it as recoverable by
	// continuing — worst case we hit a 422 on createRef and handle it there.

	// --- Step 3: fetch a curated context set of repo files ---
	slog.Info("selfupdate: fetching repo context")
	repoCtx, err := a.buildRepoContext(ctx, treeSHA)
	if err != nil {
		return nil, fmt.Errorf("build repo context: %w", err)
	}

	// --- Step 4: ask Gemini to research and propose improvements ---
	slog.Info("selfupdate: calling Gemini for research + proposals")
	prompt := a.buildPrompt(today, repoCtx)
	raw, err := a.vx.Research(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("vertex research: %w", err)
	}

	// --- Step 5: parse the model's JSON response robustly ---
	proposal, err := parseProposal(raw)
	if err != nil {
		return nil, fmt.Errorf("parse model response: %w", err)
	}

	if !proposal.Changed || len(proposal.Files) == 0 {
		slog.Info("selfupdate: model found nothing to change today")
		return &Result{Skipped: true, Summary: proposal.Rationale}, nil
	}

	// --- Step 6: apply security guardrails ---
	accepted := filterFiles(proposal.Files)
	if len(accepted) == 0 {
		slog.Info("selfupdate: all proposed files rejected by guardrails")
		return &Result{
			Skipped: true,
			Summary: "model proposed changes but all were blocked by path guardrails",
		}, nil
	}

	// --- Step 7: create blobs and build the new tree ---
	slog.Info("selfupdate: creating blobs", "count", len(accepted))
	treeNodes, changedPaths, err := a.buildTreeNodes(ctx, accepted)
	if err != nil {
		return nil, fmt.Errorf("build tree nodes: %w", err)
	}

	newTreeSHA, err := a.gh.createTree(ctx, treeSHA, treeNodes)
	if err != nil {
		return nil, fmt.Errorf("create tree: %w", err)
	}

	// --- Step 8: create the commit ---
	slog.Info("selfupdate: creating commit", "tree", newTreeSHA)
	commitSHA, err := a.gh.createCommit(ctx, proposal.PRTitle, newTreeSHA, []string{headSHA})
	if err != nil {
		return nil, fmt.Errorf("create commit: %w", err)
	}

	// --- Step 9: create the branch ref ---
	// A 422 from createRef means the ref exists (race condition or stale check).
	// We surface it as "already ran today" rather than an error, keeping the
	// caller safe from duplicate PRs.
	if err := a.gh.createRef(ctx, ref, commitSHA); err != nil {
		if strings.Contains(err.Error(), "422") {
			slog.Info("selfupdate: branch already exists (race), skipping", "branch", branchName)
			return &Result{Skipped: true, Summary: "already ran today (race)"}, nil
		}
		return nil, fmt.Errorf("create branch ref: %w", err)
	}

	// --- Step 10: open the PR ---
	slog.Info("selfupdate: opening pull request", "branch", branchName)
	prBody := buildPRBody(proposal, accepted)
	pr, err := a.gh.openPR(ctx, proposal.PRTitle, prBody, branchName, a.cfg.DefaultBranch)
	if err != nil {
		// The branch exists with the commit; just no PR. Log and surface as partial.
		slog.Error("selfupdate: PR open failed", "err", err, "branch", branchName)
		return &Result{
			Branch:       branchName,
			FilesChanged: changedPaths,
			Summary:      "branch created but PR open failed: " + err.Error(),
		}, nil
	}

	slog.Info("selfupdate: PR opened", "url", pr.HTMLURL, "number", pr.Number)
	return &Result{
		Branch:       branchName,
		PRURL:        pr.HTMLURL,
		PRNumber:     pr.Number,
		FilesChanged: changedPaths,
		Summary:      proposal.Rationale,
	}, nil
}

// --- Context building ---

// priorityFiles are always fetched in full if present.
var priorityFiles = []string{"README.md", "CHANGELOG.md", "go.mod"}

// buildRepoContext fetches the recursive tree then assembles a curated context
// string. Priority files (README/CHANGELOG/go.mod) are included verbatim.
// Go source under cmd/ and internal/ (excluding internal/selfupdate/) is
// included up to the contextCap; once the cap is reached further file contents
// are replaced with a size note to keep the prompt lean.
func (a *Agent) buildRepoContext(ctx context.Context, treeSHA string) (string, error) {
	tree, err := a.gh.getTreeRecursive(ctx, treeSHA)
	if err != nil {
		return "", fmt.Errorf("get tree: %w", err)
	}

	// Index entries by path for quick lookup.
	index := make(map[string]ghTreeEntry, len(tree.Tree))
	for _, e := range tree.Tree {
		if e.Type == "blob" {
			index[e.Path] = e
		}
	}

	var sb strings.Builder
	used := 0

	// Fetch priority files first so they are always present.
	for _, p := range priorityFiles {
		e, ok := index[p]
		if !ok {
			continue
		}
		content, err := a.fetchBlob(ctx, e)
		if err != nil {
			slog.Error("selfupdate: failed to fetch priority file", "path", p, "err", err)
			continue
		}
		fmt.Fprintf(&sb, "=== %s ===\n%s\n\n", p, content)
		used += len(content)
	}

	// Then include Go source files under cmd/ and internal/ (excluding selfupdate).
	for _, e := range tree.Tree {
		if e.Type != "blob" {
			continue
		}
		if !isSourceFile(e.Path) {
			continue
		}
		if used >= contextCap {
			fmt.Fprintf(&sb, "=== %s === [omitted, %d bytes — context cap reached]\n\n", e.Path, e.Size)
			continue
		}
		content, err := a.fetchBlob(ctx, e)
		if err != nil {
			slog.Error("selfupdate: failed to fetch source file", "path", e.Path, "err", err)
			fmt.Fprintf(&sb, "=== %s === [fetch error]\n\n", e.Path)
			continue
		}
		fmt.Fprintf(&sb, "=== %s ===\n%s\n\n", e.Path, content)
		used += len(content)
	}

	return sb.String(), nil
}

// isSourceFile returns true for *.go files under cmd/ or internal/ that are
// not part of the selfupdate package itself (to avoid circular context).
func isSourceFile(p string) bool {
	if !strings.HasSuffix(p, ".go") {
		return false
	}
	under := strings.HasPrefix(p, "cmd/") || strings.HasPrefix(p, "internal/")
	if !under {
		return false
	}
	return !strings.HasPrefix(p, "internal/selfupdate/")
}

// fetchBlob downloads a blob's content and returns it as a UTF-8 string.
// The GitHub API returns base64-encoded content; we decode it here.
func (a *Agent) fetchBlob(ctx context.Context, e ghTreeEntry) (string, error) {
	blob, err := a.gh.getBlob(ctx, e.SHA)
	if err != nil {
		return "", err
	}
	// API always returns base64; clean whitespace before decoding.
	cleaned := strings.ReplaceAll(blob.Content, "\n", "")
	b, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return "", fmt.Errorf("decode blob %s: %w", e.SHA, err)
	}
	return string(b), nil
}

// --- Prompt construction ---

func (a *Agent) buildPrompt(today, repoCtx string) string {
	return fmt.Sprintf(`You are CuriosityEngine's self-improvement agent running its daily audit (%s).

Using Google Search, research the CURRENT state of enterprise Go service engineering:
- Security advisories affecting common Go stdlib or module dependencies
- OWASP Top 10 mitigations applicable to HTTP services
- Go 1.24 best practices (error handling, slog usage, context propagation)
- Cloud Run + GCP observability recommendations (health checks, structured logging)
- Dependency hygiene: are there known CVEs in commonly-used Go modules?
- CI/CD and supply-chain hygiene (SBOM, lockfile, SAST)
- Documentation quality and accessibility of project READMEs

Then audit the following repository files and identify a SMALL, SAFE, reviewable
set of improvements. Focus on concrete, low-risk changes that a senior engineer
would be happy to approve: dependency pin updates, adding a missing header, a
small doc improvement, a missing CHANGELOG entry, or fixing an obvious gap.

Do NOT propose changes to files under internal/selfupdate/, .github/, deploy/,
Dockerfile, .dockerignore, or cloudbuild.yaml — those paths are guarded.

Repository context:
---
%s
---

Return ONLY a JSON object (no markdown fences, no prose), with this exact shape:
{
  "changed": true,
  "pr_title": "...",
  "pr_body": "...",
  "changelog_line": "...",
  "rationale": "...",
  "files": [
    {"path": "relative/path/in/repo", "content": "<full new file content>", "reason": "..."}
  ]
}

If nothing warrants a change today, return {"changed": false, "rationale": "...", "files": []}.
The "content" field must be the COMPLETE new file contents, not a diff.`,
		today, repoCtx)
}

// --- Model response parsing ---

// proposalFile is one file the model wants to modify.
type proposalFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

// proposal is the JSON shape the agent asks Gemini to return.
type proposal struct {
	Changed       bool           `json:"changed"`
	PRTitle       string         `json:"pr_title"`
	PRBody        string         `json:"pr_body"`
	ChangelogLine string         `json:"changelog_line"`
	Rationale     string         `json:"rationale"`
	Files         []proposalFile `json:"files"`
}

// parseProposal robustly extracts the first balanced {...} JSON object from the
// model's raw text, strips any ``` fences, and unmarshals it. It never panics;
// malformed output returns an error the caller handles gracefully.
func parseProposal(raw string) (*proposal, error) {
	// Strip common markdown code fences the model sometimes emits despite instructions.
	cleaned := strings.TrimSpace(raw)
	if strings.HasPrefix(cleaned, "```") {
		// Drop the first line (```json or ```) and the last ``` fence.
		lines := strings.SplitN(cleaned, "\n", 2)
		if len(lines) == 2 {
			cleaned = lines[1]
		}
		if idx := strings.LastIndex(cleaned, "```"); idx >= 0 {
			cleaned = cleaned[:idx]
		}
		cleaned = strings.TrimSpace(cleaned)
	}

	// Extract the first balanced JSON object in case the model added preamble.
	js := extractJSON(cleaned)
	if js == "" {
		return nil, fmt.Errorf("no JSON object found in model response (first 200 chars: %q)",
			truncate(cleaned, 200))
	}

	var p proposal
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		return nil, fmt.Errorf("unmarshal proposal: %w", err)
	}
	return &p, nil
}

// extractJSON returns the first balanced {...} substring in s.
// It is the same algorithm used in the engine package, duplicated here to keep
// the selfupdate package self-contained and importable independently.
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
			// inside a string: ignore structural characters
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// --- Security guardrails ---

// filterFiles drops any proposed file that the agent is not allowed to modify.
// The check is conservative by design: when in doubt, reject.
// Accepted files are returned as a new slice; the original is not modified.
func filterFiles(files []proposalFile) []proposalFile {
	out := make([]proposalFile, 0, len(files))
	for _, f := range files {
		if reason := rejectReason(f.Path); reason != "" {
			slog.Info("selfupdate: rejecting proposed file", "path", f.Path, "reason", reason)
			continue
		}
		out = append(out, f)
	}
	return out
}

// rejectReason returns a non-empty string explaining why path is rejected,
// or "" if the path is permitted. It cleans the path before checking to
// prevent traversal tricks like "internal/selfupdate/../selfupdate/github.go".
func rejectReason(p string) string {
	// Absolute paths cannot be valid relative repo paths.
	if path.IsAbs(p) {
		return "absolute path"
	}
	// Clean to collapse any ".." segments; reject if cleaning changes the path
	// (a sign of traversal) or leaves a ".." component.
	clean := path.Clean(p)
	if strings.Contains(clean, "..") {
		return "path traversal"
	}
	// Reject paths that escape the repo root.
	if strings.HasPrefix(clean, "/") {
		return "escapes repo root after cleaning"
	}

	// Check guarded prefixes.
	for _, prefix := range guardedPrefixes {
		if strings.HasPrefix(clean+"/", prefix) || strings.HasPrefix(clean, prefix) {
			return "guarded prefix: " + prefix
		}
	}

	// Check exact guarded filenames.
	if guardedExact[clean] {
		return "guarded file: " + clean
	}

	return ""
}

// --- Tree node construction ---

func (a *Agent) buildTreeNodes(ctx context.Context, files []proposalFile) ([]ghTreeNodeReq, []string, error) {
	nodes := make([]ghTreeNodeReq, 0, len(files))
	paths := make([]string, 0, len(files))

	for _, f := range files {
		blobSHA, err := a.gh.createBlob(ctx, f.Content, "utf-8")
		if err != nil {
			return nil, nil, fmt.Errorf("create blob for %s: %w", f.Path, err)
		}
		nodes = append(nodes, ghTreeNodeReq{
			Path: f.Path,
			Mode: "100644", // regular file
			Type: "blob",
			SHA:  blobSHA,
		})
		paths = append(paths, f.Path)
	}
	return nodes, paths, nil
}

// --- PR body assembly ---

// buildPRBody assembles the pull request body from the model's prose plus the
// machine-generated metadata block. The format is intentionally transparent so
// reviewers understand exactly what the agent did and why.
func buildPRBody(p *proposal, accepted []proposalFile) string {
	var sb strings.Builder

	sb.WriteString(p.PRBody)
	sb.WriteString("\n\n---\n\n")
	sb.WriteString("## Agent metadata\n\n")
	sb.WriteString("**Rationale:** ")
	sb.WriteString(p.Rationale)
	sb.WriteString("\n\n")

	if len(accepted) > 0 {
		sb.WriteString("### Files changed\n\n")
		for _, f := range accepted {
			fmt.Fprintf(&sb, "- **`%s`** — %s\n", f.Path, f.Reason)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("> This pull request was opened automatically by the CuriosityEngine ")
	sb.WriteString("self-update agent. It has not been merged and requires human review. ")
	sb.WriteString("The agent never pushes directly to the default branch.\n\n")
	sb.WriteString("🤖 Generated with [Claude Code](https://claude.com/claude-code)")

	return sb.String()
}
