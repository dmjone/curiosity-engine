#!/usr/bin/env bash
#
# deploy.sh — idempotent, zero-intervention provisioning + deploy for
# CuriosityEngine. Re-running it is safe: every resource is created only if
# missing and otherwise updated in place.
#
# It provisions a scale-to-zero Cloud Run service whose idle cost is $0, with
# a strict least-privilege IAM layout:
#   - curiosity-run        : the Cloud Run runtime identity. Holds only
#                            aiplatform.user + datastore.user, and read access
#                            to exactly its two secrets. No deploy/admin power.
#   - curiosity-scheduler  : the Cloud Scheduler identity. Holds only
#                            run.invoker on this one service.
# Vertex AI is reached through that runtime identity (IAM), never an API key.
#
# Usage:  bash deploy/deploy.sh
set -euo pipefail

# ----- configuration -------------------------------------------------------
PROJECT="${PROJECT:-dmjone}"
REGION="${REGION:-us-central1}"
SERVICE="curiosity-engine"
AR_REPO="curiosity"
DOMAIN="curiosityengine.dmj.one"
GEMINI_MODEL="${GEMINI_MODEL:-gemini-2.5-flash}"
GITHUB_OWNER="${GITHUB_OWNER:-dmjone}"
GITHUB_REPO="${GITHUB_REPO:-curiosity-engine}"
GITHUB_BRANCH="${GITHUB_BRANCH:-main}"

RUNTIME_SA="curiosity-run"
SCHED_SA="curiosity-scheduler"
DISCORD_SECRET="curiosity-discord"
GITHUB_SECRET="curiosity-github"

RUNTIME_SA_EMAIL="${RUNTIME_SA}@${PROJECT}.iam.gserviceaccount.com"
SCHED_SA_EMAIL="${SCHED_SA}@${PROJECT}.iam.gserviceaccount.com"
IMAGE="${REGION}-docker.pkg.dev/${PROJECT}/${AR_REPO}/${SERVICE}"
TAG="deploy-$(date +%Y%m%d-%H%M%S)"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

say() { printf '\n\033[1;35m==> %s\033[0m\n' "$*"; }

say "Project ${PROJECT}, region ${REGION}"
gcloud config set project "$PROJECT" >/dev/null

# ----- 1. enable APIs ------------------------------------------------------
say "Enabling required APIs"
gcloud services enable \
  run.googleapis.com \
  cloudbuild.googleapis.com \
  artifactregistry.googleapis.com \
  firestore.googleapis.com \
  aiplatform.googleapis.com \
  secretmanager.googleapis.com \
  cloudscheduler.googleapis.com \
  iamcredentials.googleapis.com \
  --quiet

# ----- 2. Artifact Registry ------------------------------------------------
say "Artifact Registry repo ${AR_REPO}"
if ! gcloud artifacts repositories describe "$AR_REPO" --location="$REGION" >/dev/null 2>&1; then
  gcloud artifacts repositories create "$AR_REPO" \
    --repository-format=docker --location="$REGION" \
    --description="CuriosityEngine container images" --quiet
fi

# ----- 3. service accounts -------------------------------------------------
say "Service accounts"
if ! gcloud iam service-accounts describe "$RUNTIME_SA_EMAIL" >/dev/null 2>&1; then
  gcloud iam service-accounts create "$RUNTIME_SA" \
    --display-name="CuriosityEngine Cloud Run runtime" --quiet
fi
if ! gcloud iam service-accounts describe "$SCHED_SA_EMAIL" >/dev/null 2>&1; then
  gcloud iam service-accounts create "$SCHED_SA" \
    --display-name="CuriosityEngine Cloud Scheduler invoker" --quiet
fi

# ----- 4. secrets ----------------------------------------------------------
# Secrets are seeded with harmless placeholders so the service starts cleanly;
# the operator overwrites them with real values afterwards. Secret Manager
# rejects a zero-byte payload, so the GitHub placeholder is a single space,
# which the service treats as "not set" (it trims to empty).
say "Secret Manager secrets"
ensure_secret() { # name, placeholder
  if ! gcloud secrets describe "$1" >/dev/null 2>&1; then
    gcloud secrets create "$1" --replication-policy=automatic --quiet
  fi
  if ! gcloud secrets versions list "$1" --limit=1 --format='value(name)' 2>/dev/null | grep -q .; then
    printf '%s' "$2" | gcloud secrets versions add "$1" --data-file=- --quiet
  fi
}
ensure_secret "$DISCORD_SECRET" '{"app_id":"","public_key":"","bot_token":"","admin_user_id":""}'
ensure_secret "$GITHUB_SECRET" ' '

# ----- 5. least-privilege IAM ---------------------------------------------
say "Granting least-privilege IAM"
# Runtime identity: Vertex AI + Firestore, nothing else.
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
  --role="roles/aiplatform.user" --condition=None --quiet >/dev/null
gcloud projects add-iam-policy-binding "$PROJECT" \
  --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
  --role="roles/datastore.user" --condition=None --quiet >/dev/null
# Secret access is granted per-secret, not project-wide.
gcloud secrets add-iam-policy-binding "$DISCORD_SECRET" \
  --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
  --role="roles/secretmanager.secretAccessor" --quiet >/dev/null
gcloud secrets add-iam-policy-binding "$GITHUB_SECRET" \
  --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
  --role="roles/secretmanager.secretAccessor" --quiet >/dev/null

# ----- 6. build the image --------------------------------------------------
say "Building image with Cloud Build"
gcloud builds submit --tag "${IMAGE}:${TAG}" --quiet
gcloud artifacts docker tags add "${IMAGE}:${TAG}" "${IMAGE}:latest" --quiet || true

# ----- 7. deploy Cloud Run (scale-to-zero) ---------------------------------
say "Deploying Cloud Run service"
gcloud run deploy "$SERVICE" \
  --image="${IMAGE}:${TAG}" \
  --region="$REGION" \
  --service-account="$RUNTIME_SA_EMAIL" \
  --allow-unauthenticated \
  --min-instances=0 \
  --max-instances=2 \
  --cpu=1 \
  --memory=512Mi \
  --cpu-boost \
  --concurrency=40 \
  --timeout=300 \
  --set-env-vars="GCP_PROJECT=${PROJECT},VERTEX_LOCATION=${REGION},GEMINI_MODEL=${GEMINI_MODEL},FIRESTORE_DATABASE=(default),GITHUB_OWNER=${GITHUB_OWNER},GITHUB_REPO=${GITHUB_REPO},GITHUB_DEFAULT_BRANCH=${GITHUB_BRANCH}" \
  --quiet

URL="$(gcloud run services describe "$SERVICE" --region="$REGION" --format='value(status.url)')"
AUDIENCE="${URL}/cron/daily"
say "Service URL: ${URL}"

# Wire the cron-auth settings now that the URL is known.
gcloud run services update "$SERVICE" --region="$REGION" --quiet \
  --update-env-vars="SCHEDULER_SA_EMAIL=${SCHED_SA_EMAIL},EXPECTED_AUDIENCE=${AUDIENCE}" >/dev/null

# ----- 8. scheduler invoker permission ------------------------------------
say "Authorizing the scheduler to invoke the service"
gcloud run services add-iam-policy-binding "$SERVICE" --region="$REGION" \
  --member="serviceAccount:${SCHED_SA_EMAIL}" \
  --role="roles/run.invoker" --quiet >/dev/null

# ----- 9. daily Cloud Scheduler job ---------------------------------------
say "Cloud Scheduler job (09:00 IST daily)"
if gcloud scheduler jobs describe curiosity-daily --location="$REGION" >/dev/null 2>&1; then
  gcloud scheduler jobs update http curiosity-daily --location="$REGION" \
    --schedule="0 9 * * *" --time-zone="Asia/Kolkata" \
    --uri="${AUDIENCE}" --http-method=POST \
    --oidc-service-account-email="$SCHED_SA_EMAIL" \
    --oidc-token-audience="$AUDIENCE" \
    --attempt-deadline=320s --quiet
else
  gcloud scheduler jobs create http curiosity-daily --location="$REGION" \
    --schedule="0 9 * * *" --time-zone="Asia/Kolkata" \
    --uri="${AUDIENCE}" --http-method=POST \
    --oidc-service-account-email="$SCHED_SA_EMAIL" \
    --oidc-token-audience="$AUDIENCE" \
    --attempt-deadline=320s --quiet
fi

# ----- 10. custom domain mapping ------------------------------------------
say "Mapping ${DOMAIN}"
if ! gcloud beta run domain-mappings describe --domain="$DOMAIN" --region="$REGION" >/dev/null 2>&1; then
  gcloud beta run domain-mappings create --service="$SERVICE" \
    --domain="$DOMAIN" --region="$REGION" --quiet || \
    echo "NOTE: domain mapping needs the DNS record below, then it self-completes."
fi

say "Done."
cat <<EOF

--------------------------------------------------------------------------
DEPLOYED.  Service URL: ${URL}

REMAINING MANUAL STEPS
1. Discord app  ->  put real values in Secret Manager secret '${DISCORD_SECRET}':
     gcloud secrets versions add ${DISCORD_SECRET} --data-file=discord.json
   where discord.json is:
     {"app_id":"...","public_key":"...","bot_token":"...","admin_user_id":"..."}
   Then set the Discord "Interactions Endpoint URL" to:
     https://${DOMAIN}/interactions

2. GitHub token ->  put a fine-grained PAT (Contents + Pull requests: write,
   scoped to ${GITHUB_OWNER}/${GITHUB_REPO}) in secret '${GITHUB_SECRET}':
     printf 'github_pat_xxx' | gcloud secrets versions add ${GITHUB_SECRET} --data-file=-

3. Cloudflare DNS (Claude cannot edit this) -- add in the dmj.one zone:
     Type: CNAME   Name: curiosityengine   Target: ghs.googlehosted.com
     Proxy: ON
   Then run, to read the exact record Google expects:
     gcloud beta run domain-mappings describe --domain=${DOMAIN} --region=${REGION}
--------------------------------------------------------------------------
EOF
