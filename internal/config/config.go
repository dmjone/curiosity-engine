// Package config loads runtime configuration from the environment.
//
// Nothing sensitive lives here: Discord credentials are kept in Secret Manager
// and fetched at runtime (see package secrets). Everything in Config is either
// public (project, region, model) or operational (scheduler identity).
package config

import (
	"context"
	"log/slog"
	"os"

	"cloud.google.com/go/compute/metadata"
)

// Config holds non-secret runtime settings.
type Config struct {
	ProjectID        string // GCP project hosting Firestore + Vertex AI
	Location         string // Vertex AI region, e.g. us-central1
	FirestoreDB      string // Firestore database id, "(default)" unless overridden
	GeminiModel      string // Gemini model id used for the daily research
	SchedulerSAEmail string // service account allowed to invoke /cron/daily
	ExpectedAudience string // OIDC audience the scheduler token must carry
	GitHubOwner      string // owner of the repo the self-update agent maintains
	GitHubRepo       string // repo name the self-update agent maintains
	GitHubBranch     string // default branch self-update PRs target
	Port             string // HTTP port (Cloud Run injects PORT)
}

// Load reads configuration from the environment, falling back to the GCE
// metadata server for the project id when running on Cloud Run.
func Load(ctx context.Context) *Config {
	c := &Config{
		ProjectID:        env("GCP_PROJECT", ""),
		Location:         env("VERTEX_LOCATION", "us-central1"),
		FirestoreDB:      env("FIRESTORE_DATABASE", "(default)"),
		GeminiModel:      env("GEMINI_MODEL", "gemini-2.5-flash"),
		SchedulerSAEmail: env("SCHEDULER_SA_EMAIL", ""),
		ExpectedAudience: env("EXPECTED_AUDIENCE", ""),
		GitHubOwner:      env("GITHUB_OWNER", "dmjone"),
		GitHubRepo:       env("GITHUB_REPO", "curiosity-engine"),
		GitHubBranch:     env("GITHUB_DEFAULT_BRANCH", "main"),
		Port:             env("PORT", "8080"),
	}
	if c.ProjectID == "" && metadata.OnGCE() {
		if p, err := metadata.ProjectIDWithContext(ctx); err == nil {
			c.ProjectID = p
		}
	}
	if c.ProjectID == "" {
		slog.Error("config: project id is empty (set GCP_PROJECT)")
	}
	return c
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
