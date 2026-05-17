// Package vertex wraps Vertex AI (Gemini) for the daily problem research.
//
// Authentication is IAM-only: the client uses Application Default Credentials,
// which on Cloud Run resolve to the service's runtime service account. There
// is no API key anywhere in this codebase. The runtime SA holds exactly
// roles/aiplatform.user, so the Vertex surface is the only AI it can reach.
package vertex

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// Client generates content with a Gemini model on the Vertex AI backend.
type Client struct {
	g     *genai.Client
	model string
}

// New constructs a Vertex-backed Gemini client.
func New(ctx context.Context, project, location, model string) (*Client, error) {
	g, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("genai client: %w", err)
	}
	return &Client{g: g, model: model}, nil
}

// Research runs a single grounded generation. The Google Search tool lets
// Gemini pull current, real competitive-programming material rather than
// hallucinating problems; that grounding is the "research" in the daily
// self-update.
func (c *Client) Research(ctx context.Context, prompt string) (string, error) {
	temp := float32(1.0)
	cfg := &genai.GenerateContentConfig{
		Temperature: &temp,
		Tools:       []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}},
	}
	res, err := c.g.Models.GenerateContent(ctx, c.model, genai.Text(prompt), cfg)
	if err != nil {
		return "", fmt.Errorf("generate content: %w", err)
	}
	text := res.Text()
	if text == "" {
		return "", fmt.Errorf("empty model response")
	}
	return text, nil
}
