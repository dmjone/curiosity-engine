#!/usr/bin/env bash
#
# teardown.sh — remove every billable or named resource deploy.sh created.
# Firestore data is left untouched (deleting a database is irreversible and
# not what a teardown should silently do); delete it by hand if you mean to.
#
# Usage:  bash deploy/teardown.sh
set -uo pipefail

PROJECT="${PROJECT:-dmjone}"
REGION="${REGION:-us-central1}"
SERVICE="curiosity-engine"
AR_REPO="curiosity"
DOMAIN="curiosityengine.dmj.one"
RUNTIME_SA_EMAIL="curiosity-run@${PROJECT}.iam.gserviceaccount.com"
SCHED_SA_EMAIL="curiosity-scheduler@${PROJECT}.iam.gserviceaccount.com"

gcloud config set project "$PROJECT" >/dev/null

echo "==> Removing scheduler job"
gcloud scheduler jobs delete curiosity-daily --location="$REGION" --quiet 2>/dev/null || true

echo "==> Removing domain mapping"
gcloud beta run domain-mappings delete --domain="$DOMAIN" --region="$REGION" --quiet 2>/dev/null || true

echo "==> Removing Cloud Run service"
gcloud run services delete "$SERVICE" --region="$REGION" --quiet 2>/dev/null || true

echo "==> Removing Artifact Registry repo"
gcloud artifacts repositories delete "$AR_REPO" --location="$REGION" --quiet 2>/dev/null || true

echo "==> Removing secrets"
gcloud secrets delete curiosity-discord --quiet 2>/dev/null || true
gcloud secrets delete curiosity-github --quiet 2>/dev/null || true

echo "==> Removing service accounts"
gcloud iam service-accounts delete "$RUNTIME_SA_EMAIL" --quiet 2>/dev/null || true
gcloud iam service-accounts delete "$SCHED_SA_EMAIL" --quiet 2>/dev/null || true

echo "==> Done. Firestore data was left intact on purpose."
