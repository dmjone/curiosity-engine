// Package selfupdate is the daily self-maintenance agent for CuriosityEngine.
//
// Once a day, when the Cloud Scheduler wakes the service, this agent researches
// current enterprise software-engineering practices and opens a Pull Request
// proposing improvements to the project's own source and docs on GitHub. It
// never pushes to the default branch directly — all proposed changes go through
// a PR so a human can review and merge (or close) them.
package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	ghAPIBase       = "https://api.github.com"
	ghAPIVersion    = "2022-11-28"
	ghUserAgent     = "CuriosityEngine-SelfUpdate/1.0"
	ghClientTimeout = 30 * time.Second
)

// ghClient is a minimal GitHub REST API v3 client built entirely on stdlib.
// It implements only the Git Data + Pull Requests surfaces that the agent needs;
// no third-party GitHub SDK is used so the binary stays lean and dependency-free
// on this path.
type ghClient struct {
	token string
	owner string
	repo  string
	http  *http.Client
}

func newGHClient(token, owner, repo string) *ghClient {
	return &ghClient{
		token: token,
		owner: owner,
		repo:  repo,
		http:  &http.Client{Timeout: ghClientTimeout},
	}
}

// do executes one API call, attaches auth headers, and returns the parsed body.
// Non-2xx responses are turned into an error that includes the status code and
// the first 512 bytes of the body so callers have enough context to diagnose.
func (c *ghClient) do(ctx context.Context, method, path string, reqBody, dst any) error {
	var rdr io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, ghAPIBase+path, rdr)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", ghAPIVersion)
	req.Header.Set("User-Agent", ghUserAgent)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // cap at 1 MiB
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(body)
		if len(snippet) > 512 {
			snippet = snippet[:512]
		}
		return fmt.Errorf("github %s %s: status %d: %s", method, path, resp.StatusCode, snippet)
	}
	if dst != nil && len(body) > 0 {
		if err := json.Unmarshal(body, dst); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return nil
}

// repoPath returns the /repos/:owner/:repo prefix used by most API routes.
func (c *ghClient) repoPath() string {
	return "/repos/" + c.owner + "/" + c.repo
}

// --- Response types (only the fields the agent actually reads) ---

type ghRef struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type ghCommit struct {
	SHA  string `json:"sha"`
	Tree struct {
		SHA string `json:"sha"`
	} `json:"tree"`
}

type ghTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int    `json:"size"`
	URL  string `json:"url"`
}

type ghTree struct {
	SHA       string        `json:"sha"`
	Truncated bool          `json:"truncated"`
	Tree      []ghTreeEntry `json:"tree"`
}

type ghBlob struct {
	Content  string `json:"content"`  // base64-encoded
	Encoding string `json:"encoding"` // always "base64" from the API
	SHA      string `json:"sha"`
}

type ghCreateBlobReq struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type ghCreatedSHA struct {
	SHA string `json:"sha"`
}

type ghTreeNodeReq struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	SHA     string `json:"sha,omitempty"`
	Content string `json:"content,omitempty"`
}

type ghCreateTreeReq struct {
	BaseTree string          `json:"base_tree"`
	Tree     []ghTreeNodeReq `json:"tree"`
}

type ghCreateCommitReq struct {
	Message   string           `json:"message"`
	Tree      string           `json:"tree"`
	Parents   []string         `json:"parents"`
	Author    ghCommitIdentity `json:"author"`
	Committer ghCommitIdentity `json:"committer"`
}

type ghCommitIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type ghCreateRefReq struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type ghPatchRefReq struct {
	SHA   string `json:"sha"`
	Force bool   `json:"force"`
}

type ghCreatePRReq struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}

type ghPR struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

// --- API methods ---

// getRef resolves a fully-qualified ref (e.g. "refs/heads/main") to its SHA.
func (c *ghClient) getRef(ctx context.Context, ref string) (*ghRef, error) {
	// The API path strips the leading "refs/" prefix.
	path := "/git/refs/" + ref[len("refs/"):]
	var out ghRef
	if err := c.do(ctx, http.MethodGet, c.repoPath()+path, nil, &out); err != nil {
		return nil, fmt.Errorf("get ref %s: %w", ref, err)
	}
	return &out, nil
}

// getCommit fetches commit metadata including the tree SHA.
func (c *ghClient) getCommit(ctx context.Context, sha string) (*ghCommit, error) {
	var out ghCommit
	if err := c.do(ctx, http.MethodGet, c.repoPath()+"/git/commits/"+sha, nil, &out); err != nil {
		return nil, fmt.Errorf("get commit %s: %w", sha, err)
	}
	return &out, nil
}

// getTreeRecursive fetches the full (recursive) tree for a given tree SHA.
// Large trees may be truncated by the API; the caller reads the Truncated flag.
func (c *ghClient) getTreeRecursive(ctx context.Context, treeSHA string) (*ghTree, error) {
	var out ghTree
	path := c.repoPath() + "/git/trees/" + treeSHA + "?recursive=1"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("get tree %s: %w", treeSHA, err)
	}
	return &out, nil
}

// getFileContents fetches and base64-decodes a file via the Contents API.
// It returns the raw bytes. For large files, prefer getBlob via the SHA.
func (c *ghClient) getFileContents(ctx context.Context, filePath string) (*ghBlob, error) {
	var out ghBlob
	path := c.repoPath() + "/contents/" + filePath
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("get contents %s: %w", filePath, err)
	}
	return &out, nil
}

// getBlob fetches a git blob by its SHA. Content is returned base64-encoded.
func (c *ghClient) getBlob(ctx context.Context, blobSHA string) (*ghBlob, error) {
	var out ghBlob
	if err := c.do(ctx, http.MethodGet, c.repoPath()+"/git/blobs/"+blobSHA, nil, &out); err != nil {
		return nil, fmt.Errorf("get blob %s: %w", blobSHA, err)
	}
	return &out, nil
}

// createBlob uploads content and returns the new blob SHA.
// Encoding must be "utf-8" or "base64".
func (c *ghClient) createBlob(ctx context.Context, content, encoding string) (string, error) {
	req := ghCreateBlobReq{Content: content, Encoding: encoding}
	var out ghCreatedSHA
	if err := c.do(ctx, http.MethodPost, c.repoPath()+"/git/blobs", req, &out); err != nil {
		return "", fmt.Errorf("create blob: %w", err)
	}
	return out.SHA, nil
}

// createTree creates a new tree rooted at baseSHA with the given nodes.
// Returns the SHA of the new tree.
func (c *ghClient) createTree(ctx context.Context, baseSHA string, nodes []ghTreeNodeReq) (string, error) {
	req := ghCreateTreeReq{BaseTree: baseSHA, Tree: nodes}
	var out ghCreatedSHA
	if err := c.do(ctx, http.MethodPost, c.repoPath()+"/git/trees", req, &out); err != nil {
		return "", fmt.Errorf("create tree: %w", err)
	}
	return out.SHA, nil
}

// createCommit creates a commit and returns its SHA.
func (c *ghClient) createCommit(ctx context.Context, message, treeSHA string, parents []string) (string, error) {
	identity := ghCommitIdentity{
		Name:  "CuriosityEngine SelfUpdate",
		Email: "selfupdate@curiosityengine.dmj.one",
	}
	req := ghCreateCommitReq{
		Message:   message,
		Tree:      treeSHA,
		Parents:   parents,
		Author:    identity,
		Committer: identity,
	}
	var out ghCreatedSHA
	if err := c.do(ctx, http.MethodPost, c.repoPath()+"/git/commits", req, &out); err != nil {
		return "", fmt.Errorf("create commit: %w", err)
	}
	return out.SHA, nil
}

// createRef creates a new branch ref pointing to sha.
// Returns a ghClient-level error that callers can inspect to detect
// the "already exists" (422) case and treat as idempotent.
func (c *ghClient) createRef(ctx context.Context, ref, sha string) error {
	req := ghCreateRefReq{Ref: ref, SHA: sha}
	if err := c.do(ctx, http.MethodPost, c.repoPath()+"/git/refs", req, nil); err != nil {
		return fmt.Errorf("create ref %s: %w", ref, err)
	}
	return nil
}

// updateRef force-moves an existing ref to sha.
func (c *ghClient) updateRef(ctx context.Context, ref, sha string) error {
	// Strip "refs/" prefix: PATCH /git/refs/:ref uses e.g. "heads/auto/..."
	path := "/git/refs/" + ref[len("refs/"):]
	req := ghPatchRefReq{SHA: sha, Force: true}
	if err := c.do(ctx, http.MethodPatch, c.repoPath()+path, req, nil); err != nil {
		return fmt.Errorf("update ref %s: %w", ref, err)
	}
	return nil
}

// openPR opens a pull request and returns the PR number and HTML URL.
func (c *ghClient) openPR(ctx context.Context, title, body, head, base string) (*ghPR, error) {
	req := ghCreatePRReq{Title: title, Body: body, Head: head, Base: base}
	var out ghPR
	if err := c.do(ctx, http.MethodPost, c.repoPath()+"/pulls", req, &out); err != nil {
		return nil, fmt.Errorf("open PR: %w", err)
	}
	return &out, nil
}
