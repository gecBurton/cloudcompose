#!/usr/bin/env bash
#
# End-to-end smoke test against real AWS.
#
# Deploys the bootstrap environment (VPC + shared ALB + ECS cluster), compiles a
# compose file with composey, deploys the resulting app, then polls the ALB URL
# until it serves a page. EVERYTHING is torn down on exit (success or failure)
# via a trap, so a crashed run does not leave NAT gateways billing.
#
# Usage:
#   scripts/smoke-test.sh
#   PROFILE=personal COMPOSE=examples/hello/compose.yml scripts/smoke-test.sh
#   PROFILE= scripts/smoke-test.sh        # empty PROFILE: use ambient creds (CI)
#   KEEP=1 scripts/smoke-test.sh          # skip teardown to inspect resources
#
# Requires: terraform, uv (composey), curl. Locally also aws-vault (PROFILE names
# the profile); in CI set PROFILE= (empty) to run terraform with ambient AWS
# credentials (e.g. an OIDC-assumed role).
#
set -euo pipefail

# --- Config (override via environment) --------------------------------------
PROFILE="${PROFILE-personal}"                        # aws-vault profile; empty = ambient creds
NAME="${NAME:-smoke}"                                 # environment name
COMPOSE="${COMPOSE:-examples/hello/compose.yml}"      # app to deploy
PROJECT="${PROJECT:-hello}"                            # composey project name
HTTP_PATH="${HTTP_PATH:-/}"                            # path to poll on the ALB
EXPECT="${EXPECT:-Server name}"                        # string expected in HTTP body
POLL_TIMEOUT="${POLL_TIMEOUT:-300}"                    # seconds to wait for healthy ALB
                                                       # (ALB default health check needs
                                                       # 5×30s + Fargate cold start)
KEEP="${KEEP:-0}"                                      # 1 = do not destroy afterwards

# Resolve paths relative to the repo root (this script lives in scripts/).
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BOOTSTRAP_DIR="$ROOT/bootstrap"
BUILD_DIR="$ROOT/build/$PROJECT"

# With a profile, wrap terraform in aws-vault (local); without one, run it
# directly against the ambient AWS credentials (CI / OIDC-assumed role).
if [[ -n "$PROFILE" ]]; then
  TF="aws-vault exec $PROFILE -- terraform"
else
  TF="terraform"
fi

log()  { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }
fail() { printf '\n\033[1;31mFAIL:\033[0m %s\n' "$*" >&2; exit 1; }

# --- Teardown ----------------------------------------------------------------
# Runs on ANY exit. Destroy app first (depends on bootstrap), then bootstrap.
cleanup() {
  local status=$?
  if [[ "$KEEP" == "1" ]]; then
    log "KEEP=1 set — leaving resources up. Destroy later with:"
    echo "  (cd $BUILD_DIR && $TF destroy -auto-approve)"
    echo "  (cd $BOOTSTRAP_DIR && $TF destroy -auto-approve -var name=$NAME)"
    exit $status
  fi
  log "Tearing down (exit status $status)…"
  if [[ -d "$BUILD_DIR" ]]; then
    (cd "$BUILD_DIR" && eval "$TF destroy -auto-approve") \
      || echo "WARNING: app destroy failed — CHECK THE CONSOLE for orphaned resources."
  fi
  (cd "$BOOTSTRAP_DIR" && eval "$TF destroy -auto-approve -var name=$NAME") \
    || echo "WARNING: bootstrap destroy failed — CHECK NAT GATEWAYS / EIPs / ALB manually."
  exit $status
}
trap cleanup EXIT

# --- 1. Deploy bootstrap -----------------------------------------------------
log "Deploying bootstrap environment '$NAME'…"
cd "$BOOTSTRAP_DIR"
eval "$TF init -input=false"
eval "$TF apply -auto-approve -var name=$NAME"

ALB_DNS="$(eval "$TF output -raw alb_dns_name")"
[[ -n "$ALB_DNS" ]] || fail "bootstrap produced no alb_dns_name"
log "Environment up. ALB: $ALB_DNS"

# --- 2. Compile the app ------------------------------------------------------
log "Compiling $COMPOSE with composey…"
cd "$ROOT"
uv run composey -f "$COMPOSE" -e "$BOOTSTRAP_DIR/environment.yml" -p "$PROJECT" -o "$BUILD_DIR"

# --- 3. Deploy the app -------------------------------------------------------
log "Deploying app '$PROJECT'…"
cd "$BUILD_DIR"
eval "$TF init -input=false"
eval "$TF apply -auto-approve"

# --- 4. Poll the ALB until it serves the app ---------------------------------
url="http://$ALB_DNS$HTTP_PATH"
log "Polling $url (up to ${POLL_TIMEOUT}s)…"
deadline=$(( SECONDS + POLL_TIMEOUT ))
served=0
body=""
while (( SECONDS < deadline )); do
  # No -f: capture non-2xx bodies (e.g. a 503 /health) for the EXPECT match and
  # for diagnostics on timeout.
  body="$(curl -sS --max-time 5 "$url" 2>/dev/null || true)"
  if [[ "$body" == *"$EXPECT"* ]]; then
    served=1
    break
  fi
  printf '.'
  sleep 5
done
if (( served != 1 )); then
  echo
  echo "----- last response (for diagnosis) -----"
  echo "${body:-<no response>}" | head -20
  fail "timed out after ${POLL_TIMEOUT}s waiting for '$EXPECT' at $url"
fi

log "App is live — response contains '$EXPECT'. 🎉"
echo "----- response -----"
echo "$body" | head -20

# --- 5. Managed-resource assertions ------------------------------------------
# If composey substituted an S3 bucket (minio), prove it landed in real AWS and
# that host injection reached the deployed task. Driven off applied TF state.
if grep -q '"aws_s3_bucket"' "$BUILD_DIR/main.tf.json"; then
  log "Asserting S3 substitution against applied AWS state…"
  ( cd "$BUILD_DIR" && eval "$TF show -json" ) | python3 "$ROOT/scripts/assert_s3.py" \
    || fail "S3 substitution assertions failed"
fi

log "SUCCESS — everything verified on real AWS."
exit 0
