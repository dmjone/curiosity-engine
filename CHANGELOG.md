# Changelog

All notable changes to CuriosityEngine are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning follows [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-05-17

### Added

- **Scale-to-zero Cloud Run deployment**: single Go 1.24 binary serving all routes from one process, min-instances=0 so idle cost is $0.
- **Discord HTTP-interaction bot**: Ed25519-verified `POST /interactions` endpoint handles slash commands and button interactions without an always-on gateway connection.
- **Daily Vertex AI problem research**: `POST /cron/daily` (OIDC-verified, invoked by Cloud Scheduler) uses Gemini via IAM-authenticated Vertex AI to generate one syllabus-aligned problem per subject channel each day.
- **Firestore leaderboard and streak tracking**: serverless Firestore stores per-student scores, streaks (with daily decay), and problem history; public leaderboard rendered server-side at `GET /`.
- **Self-maintaining GitHub PR agent**: the daily run includes an agent that researches current engineering standards and opens a GitHub Pull Request improving the project source and docs; the agent cannot push to `main` and cannot edit its own operational guardrails.
- **Separate-IAM security model**: dedicated `curiosity-runtime` and `curiosity-scheduler` service accounts with minimum necessary roles; no API keys anywhere; secrets (Discord credentials, GitHub PAT) stored in Secret Manager and fetched at runtime.
- **Custom domain mapping**: service reachable at `curiosityengine.dmj.one` via Cloudflare proxy and Cloud Run domain mapping.
- **CI pipeline** (`.github/workflows/ci.yml`): GitHub Actions runs `go vet`, `go build`, `go test`, and CodeQL SAST on every PR and push to `main`.
- **CD pipeline** (`cloudbuild.yaml`): Cloud Build trigger on merge to `main` builds the Docker image, pushes it to Artifact Registry (SHA + latest tags), and deploys to Cloud Run without overwriting existing service config.
- **Configuration** (`.env.example`): documented environment variables with defaults; secrets excluded by design.
- **Liveness probe**: `GET /health` for Cloud Run health checks.
