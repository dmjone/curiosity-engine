# CuriosityEngine

Faculty cannot make students care about compilers. A leaderboard can.

CuriosityEngine is a Discord bot that turns every CSE subject channel into a daily competitive arena. Each morning it researches a syllabus-aligned problem, posts it to the right channel, tracks who solved it first, decays streaks for the idle, and renders a live leaderboard. It runs on Google Cloud Run at **$0 idle cost**: the container only exists while a request is being served.

---

## The Problem

Faculty cannot generate interest in subjects. Passive lectures, no peer pressure, no feedback loop. Students disengage weeks into the semester and never recover.

## How It Works

Once a day, Cloud Scheduler sends a single authenticated request to `/cron/daily`. The service wakes up (in ~1 second), runs the daily loop, and shuts back down:

1. **Research**: Vertex AI (Gemini) reads the official syllabus and generates one problem per subject channel.
2. **Post**: Problems go to Discord via HTTP interactions, no always-on gateway required.
3. **Score**: When students respond, Ed25519-verified webhook calls update Firestore in real time.
4. **Decay**: Streaks for non-participants drop. The leaderboard is public and ruthless.
5. **Self-maintain**: A second agent researches current engineering standards and opens a GitHub Pull Request improving the project. It never pushes to `main`.

## Architecture

| Component | Why it costs $0 at rest |
|---|---|
| Cloud Run (min-instances=0) | Billed per 100ms of CPU, not per hour. Zero requests = zero cost. |
| Firestore (serverless) | Billed per read/write operation, not per GB provisioned. |
| Secret Manager | Two secrets, two accesses per day. Fits free tier. |
| Vertex AI (Gemini) | Billed per token. One daily run costs pennies. |
| Cloud Scheduler | One job, one invocation per day. Free tier covers it. |
| Cloud Build | Triggered on merge. Free tier: 120 build-minutes/day. |

The only meaningful metered cost is Vertex AI token usage during the daily run, typically a few cents per day.

### Public surfaces

```
POST /interactions   Discord HTTP-interaction webhook (Ed25519 verified)
POST /cron/daily     Daily trigger from Cloud Scheduler (OIDC verified)
GET  /               Server-rendered leaderboard
GET  /health         Liveness probe
```

### Self-maintaining agent safety model

The self-update agent runs inside the same Cloud Run instance. It can read source files and open GitHub Pull Requests. It cannot:

- Push to `main` directly (branch protection enforced at GitHub).
- Edit its own guardrails (the files governing PR scope are in the PR diff, visible to reviewers).
- Access production secrets (Secret Manager IAM binds only to the runtime service account, which never runs locally or in CI).

Every self-update PR is a normal GitHub PR: reviewable, rejectable, and auditable.

## Security and IAM

Two service accounts, minimal roles, no API keys anywhere:

| Account | Role | Purpose |
|---|---|---|
| `curiosity-runtime@...` | `roles/aiplatform.user`, `roles/datastore.user`, `secretAccessor` (scoped to 2 secrets) | Runs the Cloud Run service |
| `curiosity-scheduler@...` | `roles/run.invoker` | Allows Cloud Scheduler to call `/cron/daily` only |

Vertex AI is authenticated via Workload Identity (ADC), not an API key. Discord interactions are verified with Ed25519 signatures. Scheduler calls are verified with Google-signed OIDC tokens.

## Local Development

```bash
# 1. Copy the example env file and fill in your values
cp .env.example .env

# 2. Authenticate with GCP (uses Application Default Credentials)
gcloud auth application-default login

# 3. Run the service
go run ./cmd/server
```

The service starts on `PORT` (default 8080). For Discord interactions locally, use a tool like `ngrok` to expose a public HTTPS URL and point your Discord app's Interactions Endpoint URL at it.

## Deploy

Deployment is automated: merge to `main` triggers the [Cloud Build pipeline](cloudbuild.yaml), which builds the image, pushes it to Artifact Registry, and deploys to Cloud Run.

For first-time infrastructure setup, use the deploy script:

```bash
bash deploy/deploy.sh
```

The script is idempotent: safe to rerun on an existing deployment.

## Manual One-Time Steps

These cannot be automated because they involve external accounts:

1. **Create a Discord application** at [discord.com/developers](https://discord.com/developers/applications). Note the Application ID, Public Key, and Bot Token.
2. **Populate Secret Manager**: create two secrets in your GCP project:
   - `curiosity-discord`: a JSON object with keys `app_id`, `public_key`, `bot_token`, `admin_user_id`.
   - `curiosity-github`: a GitHub Personal Access Token with `repo` scope (used by the self-update agent to open PRs).
3. **Add the Cloudflare DNS record**: after Cloud Run assigns a domain, add a CNAME:
   ```
   curiosityengine.dmj.one  CNAME  <cloud-run-domain>  (proxied)
   ```
   Then map the custom domain in the Cloud Run console.
4. **Set the Discord Interactions Endpoint URL** to `https://curiosityengine.dmj.one/interactions`.

---

Built on Go 1.24, deployed on Google Cloud Run, proxied through Cloudflare.
