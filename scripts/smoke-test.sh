#!/usr/bin/env bash
#
# End-to-end smoke test against real AWS or Azure.
#
# Creates environment with 'cloudcompose init', compiles a compose file,
# deploys the resulting app, then polls its public URL until it serves a
# page. EVERYTHING is torn down on exit (success or failure) via a trap,
# so a crashed run does not leave NAT gateways/Container Apps
# environments billing.
#
# Unifies what used to be two near-identical scripts
# (smoke-test.sh/smoke-test-azure.sh) into one, parameterized by
# PROVIDER: the actual test flow (build -> init -> apply -> compile ->
# deploy -> poll -> assert -> teardown) is identical between clouds; only
# a handful of genuinely cloud-specific things differ (auth mechanism,
# remote-state backend shape, what URL to poll, the known Azure
# provider-bug retry below), and those are isolated into their own
# functions/branches rather than duplicated across two files.
#
# Usage:
#   scripts/smoke-test.sh                                    # AWS, default
#   PROVIDER=azure scripts/smoke-test.sh
#   PROFILE=personal COMPOSE=examples/hello/compose.yml scripts/smoke-test.sh
#   PROFILE= scripts/smoke-test.sh        # empty PROFILE: use ambient AWS creds (CI)
#   KEEP=1 scripts/smoke-test.sh          # skip teardown to inspect resources
#   STATE_BUCKET=... NAME=ci42 scripts/smoke-test.sh --destroy-only            # AWS
#   PROVIDER=azure STATE_RG=... NAME=ci42 scripts/smoke-test.sh --destroy-only # Azure
#                                         # tear down a run that leaked
#
# Requires: terraform, go, curl, python3 (for assert_managed.py only).
# Azure also requires the az CLI. Locally also aws-vault for AWS (PROFILE
# names the profile); in CI set PROFILE= (empty) to run terraform with
# ambient AWS credentials (e.g. an OIDC-assumed role). Azure's own
# credential discovery is left to Terraform itself (ARM_CLIENT_ID/etc. in
# CI, `az login` locally) -- see the "Azure authentication" comment below.
#
set -euo pipefail

# --- Config (override via environment) --------------------------------------
PROVIDER="${PROVIDER:-aws}"                            # aws or azure
PROFILE="${PROFILE-personal}"                          # aws-vault profile (AWS only); empty = ambient creds
NAME="${NAME:-smoke}"                                   # environment name
COMPOSE="${COMPOSE:-examples/hello/compose.yml}"        # app to deploy
PROJECT="${PROJECT:-hello}"                              # cloudcompose project name
HTTP_PATH="${HTTP_PATH:-/}"                              # path to poll
EXPECT="${EXPECT:-Server name}"                          # string expected in HTTP body
POLL_TIMEOUT="${POLL_TIMEOUT:-300}"                      # seconds to wait for a healthy app
                                                         # (AWS: ALB default health check needs
                                                         # 5×30s + Fargate cold start)
FRONTDOOR_POLL_TIMEOUT="${FRONTDOOR_POLL_TIMEOUT:-900}"  # seconds to wait for Front Door's own
                                                         # edge/DNS propagation (Azure only, only
                                                         # when a service has cdn:true) -- kept
                                                         # separate from POLL_TIMEOUT since it's a
                                                         # genuinely different, longer-tailed delay
                                                         # (Microsoft's own guidance: "a few minutes
                                                         # up to 10"), confirmed against a real run
                                                         # 2026-08-12 that exceeded 480s.
KEEP="${KEEP:-0}"                                        # 1 = do not destroy afterwards

# Region to deploy into. AWS default matches examples/hello/environment.yaml
# and scripts/ci-environment.aws.yaml; Azure default avoids known
# region-restricted managed-service capacity issues:
#
# Not eastus: this subscription is offer-restricted there for PostgreSQL
# Flexible Server, which fails with LocationIsOfferRestricted twenty minutes
# into an apply.
#
# Not uksouth or northeurope for anything touching Azure Managed Redis: both
# have failed Balanced_B0/B1 creation with InsufficientCapacity (confirmed
# against real Azure 2026-08-04). francecentral has neither restriction and
# was confirmed clean for both Postgres and Redis the same day -- prefer it
# whenever the example includes a cache.
if [[ "$PROVIDER" == "azure" ]]; then
  REGION="${REGION:-francecentral}"
else
  REGION="${REGION:-eu-west-2}"
fi

# Remote state. AWS uses an S3 backend (bucket+region); Azure uses an
# Azure Blob Storage backend (resource group+storage account+container) --
# genuinely different shapes, not just different names for the same two
# fields, so both variable sets exist but only the relevant ones are used
# per provider.
STATE_BUCKET="${STATE_BUCKET:-}"                         # S3 bucket for remote state (AWS); empty = local
STATE_REGION="${STATE_REGION:-eu-west-2}"                # region of STATE_BUCKET (AWS)
STATE_TABLE="${STATE_TABLE:-}"                           # DynamoDB lock table for STATE_BUCKET (AWS); empty = unlocked
STATE_RG="${STATE_RG:-}"                                 # Resource Group for remote state (Azure); empty = local
STATE_ACCOUNT="${STATE_ACCOUNT:-cloudcomposeacceptstate}"    # storage account holding state (Azure)
STATE_CONTAINER="${STATE_CONTAINER:-tfstate}"            # blob container within it (Azure)

# Resolve paths relative to the repo root (this script lives in scripts/).
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# ENV_DIR and BUILD_DIR are no longer passed to cloudcompose via an
# output-location flag -- init/compile don't take one (see
# docs/authored-environment-config.md's "no -o/--output flag" note).
# Both are instead derived here to exactly match what init/compile derive
# themselves, so this script can still cd/destroy/reference them: init
# always writes to <dir of its -e>/env-<name>, so ENV_DIR is
# computed from ENV_CONFIG_DIR (below, where the generated environment.yaml
# lives) and NAME the same way. compile always writes to
# <dir of its -f>/app-<environment name>-<project name>, so the app's
# compose file is first copied into its own build/ subdirectory
# (APP_BUILD_SRC below) purely so that output lands under build/ rather
# than inside examples/ itself.
ENV_CONFIG_DIR="$ROOT/build/$PROVIDER"                    # holds the generated environment.yaml
ENV_DIR="$ENV_CONFIG_DIR/env-$NAME"                       # generated by 'cloudcompose init'
APP_BUILD_SRC="$ROOT/build/$PROVIDER/$PROJECT-src"        # copy of the compose file's own directory
BUILD_DIR="$APP_BUILD_SRC/app-$NAME-$PROJECT"             # generated by 'cloudcompose compile'
CLOUDCOMPOSE="$ROOT/cloudcompose-go/cloudcompose"

# AWS: with a profile, wrap terraform in aws-vault (local); without one,
# run it directly against ambient credentials (CI / OIDC-assumed role).
# Azure: Terraform's own credential discovery is left alone deliberately
# -- see "Azure authentication" below.
if [[ "$PROVIDER" == "aws" && -n "$PROFILE" ]]; then
  TF="aws-vault exec $PROFILE -- terraform"
else
  TF="terraform"
fi

log()  { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }
fail() { printf '\n\033[1;31mFAIL:\033[0m %s\n' "$*" >&2; exit 1; }

# --- Azure authentication ----------------------------------------------------
# In CI the ARM_CLIENT_ID/SECRET/TENANT_ID environment variables authenticate
# as the service principal; on a developer machine none are set and
# Terraform falls back to `az login`.
#
# Do not force ARM_USE_CLI=true here. The azurerm *backend* rejects a CLI
# session owned by a service principal outright ("Authenticating using the
# Azure CLI is only supported as a User"), even though the provider tolerates
# it -- so forcing CLI auth breaks remote state in CI while appearing to work
# locally.

# --- Remote state ------------------------------------------------------------
# State on a CI runner is ephemeral: if the run is cancelled or the runner
# dies, a local state file dies with it and everything it created (NAT
# gateway, ALB, RDS / Container Apps environment...) bills forever with no
# way to `terraform destroy` it. When the provider's state variable(s) are
# set we drop a remote backend into each state directory, keyed by run
# NAME, so a leaked run stays destroyable from any machine. Without it we
# fall back to local state, which is fine on a laptop where the files
# persist.
write_backend() {
  local dir="$1" key="$2"
  case "$PROVIDER" in
    aws)
      [[ -n "$STATE_BUCKET" ]] || return 0
      # STATE_TABLE is optional -- omitted (unlocked) rather than an
      # empty dynamodb_table string, mirroring
      # aws.GenerateAwsEnvironment's own choice for real cloudcompose
      # backends (see docs/multi-user-state.md). Terraform itself treats
      # an empty string differently from an absent key.
      local dynamodb_table_line=""
      if [[ -n "$STATE_TABLE" ]]; then
        dynamodb_table_line="    dynamodb_table = \"$STATE_TABLE\""
      fi
      cat > "$dir/backend_ci.tf" <<TF
# Generated by scripts/smoke-test.sh -- not checked in.
terraform {
  backend "s3" {
    bucket  = "$STATE_BUCKET"
    key     = "$key"
    region  = "$STATE_REGION"
    encrypt = true
$dynamodb_table_line
  }
}
TF
      ;;
    azure)
      [[ -n "$STATE_RG" ]] || return 0
      # Falling back to local state here is what previously made a leaked
      # run unrecoverable, so a half-configured backend is an error, not a
      # warning.
      [[ -n "$STATE_ACCOUNT" ]] || fail "STATE_RG is set but STATE_ACCOUNT is empty: refusing to fall back to local state"
      # The storage account has shared-key access disabled, so the backend
      # must authenticate with Entra ID (use_azuread_auth) rather than an
      # account key. That works from the CI service principal and from a
      # developer's az login alike, provided the identity holds Storage
      # Blob Data Contributor on the account. See ci/README.md.
      cat > "$dir/backend_ci.tf" <<TF
# Generated by scripts/smoke-test.sh -- not checked in.
terraform {
  backend "azurerm" {
    resource_group_name  = "$STATE_RG"
    storage_account_name = "$STATE_ACCOUNT"
    container_name       = "$STATE_CONTAINER"
    key                  = "$key"
    use_azuread_auth     = true
  }
}
TF
      ;;
  esac
}

state_configured() {
  case "$PROVIDER" in
    aws) [[ -n "$STATE_BUCKET" ]] ;;
    azure) [[ -n "$STATE_RG" ]] ;;
  esac
}

state_hint() {
  state_configured || return 0
  case "$PROVIDER" in
    aws)
      echo "  State kept at s3://$STATE_BUCKET/acceptance/$NAME/ -- destroy later with:"
      if [[ -n "$STATE_TABLE" ]]; then
        echo "    STATE_BUCKET=$STATE_BUCKET STATE_TABLE=$STATE_TABLE NAME=$NAME PROJECT=$PROJECT scripts/smoke-test.sh --destroy-only"
      else
        echo "    STATE_BUCKET=$STATE_BUCKET NAME=$NAME PROJECT=$PROJECT scripts/smoke-test.sh --destroy-only"
      fi
      ;;
    azure)
      echo "  State kept at $STATE_ACCOUNT/$STATE_CONTAINER/acceptance/$NAME/ -- destroy later with:"
      echo "    PROVIDER=azure STATE_RG=$STATE_RG NAME=$NAME PROJECT=$PROJECT scripts/smoke-test.sh --destroy-only"
      ;;
  esac
}

# Azure only: state that describes no resources is worse than useless --
# --destroy-only against it reports success having done nothing, which is
# the failure mode the backend exists to prevent. A failed teardown keeps
# its state, because that is the recovery path. The account has blob
# versioning and 30-day soft delete, so this is reversible if a destroy
# reported success while leaving something behind. AWS has no equivalent
# step: S3 objects for a torn-down run are harmless to leave and get
# cleaned up by the bucket's own lifecycle rule.
purge_state() {
  [[ "$PROVIDER" == "azure" ]] || return 0
  [[ -n "$STATE_RG" && -n "$STATE_ACCOUNT" ]] || return 0
  az storage blob delete-batch \
    --account-name "$STATE_ACCOUNT" \
    --source "$STATE_CONTAINER" \
    --pattern "acceptance/$NAME/*" \
    --auth-mode login >/dev/null 2>&1 \
    || echo "  NOTE: could not remove state under acceptance/$NAME/ -- harmless, prune later."
}

# --- Teardown ----------------------------------------------------------------
# Runs on ANY exit, including cancellation. Destroy app first (it depends on
# environment), then environment.
CLEANED=0
cleanup() {
  local status=$?
  # INT/TERM run this and then fall through to the EXIT trap; only act once.
  [[ "$CLEANED" == "1" ]] && exit $status
  CLEANED=1

  if [[ "$KEEP" == "1" ]]; then
    log "KEEP=1 set — leaving resources up. Destroy later with:"
    echo "  (cd $BUILD_DIR && $TF destroy -auto-approve)"
    echo "  (cd $ENV_DIR && $TF destroy -auto-approve)"
    exit $status
  fi

  log "Tearing down (exit status $status)…"

  # Capture logs as a permanent CI artifact before anything is destroyed,
  # regardless of how this run ended -- a crash mid-poll or a failed
  # apply is exactly when this is most useful, and by definition the
  # in-flow show_diagnostics calls above never got to run. Best-effort:
  # requires the app to have actually been deployed ($BUILD_DIR/main.tf.json
  # exists, i.e. `cloudcompose main` itself ran) and $CLOUDCOMPOSE to have
  # been built; a run that failed before either of those has nothing to
  # show logs for, not a new failure to report. The same guard also
  # guarantees $COMPOSE_BUILD_COPY is set (compile, which sets it, must
  # have already run for main.tf.json to exist) -- see show_diagnostics'
  # own comment below for why that matters, not just $COMPOSE.
  if [[ -x "$CLOUDCOMPOSE" && -f "$BUILD_DIR/main.tf.json" ]]; then
    log "Final logs snapshot before teardown…"
    "$CLOUDCOMPOSE" logs -f "$COMPOSE_BUILD_COPY" -e "$ENV_DIR" -p "$PROJECT" --tail 500 || true
  fi

  local leaked=0
  if [[ -d "$BUILD_DIR" ]]; then
    (cd "$BUILD_DIR" && eval "$TF destroy -auto-approve") \
      || { leaked=1; echo "WARNING: app destroy failed — CHECK THE CONSOLE for orphaned resources."; }
  fi
  if [[ -d "$ENV_DIR" ]]; then
    (cd "$ENV_DIR" && eval "$TF destroy -auto-approve") \
      || { leaked=1; echo "WARNING: environment destroy failed — CHECK NAT GATEWAYS/EIPs/ALB or the RESOURCE GROUP manually."; }
  fi

  if (( leaked == 1 )); then
    state_hint
  else
    purge_state
  fi
  exit $status
}
trap cleanup EXIT INT TERM

# --- Destroy-only mode -------------------------------------------------------
# Recovery path for a run that leaked: re-point at its remote state and tear
# it down. Requires the provider's state variable(s) and the NAME the leaked
# run used.
if [[ "${1:-}" == "--destroy-only" ]]; then
  state_configured || fail "--destroy-only needs remote state configured (STATE_BUCKET for AWS, STATE_RG for Azure) -- runs with local state are not recoverable"
  log "Destroy-only mode for environment '$NAME' ($PROVIDER)."

  write_backend "$ENV_DIR" "acceptance/$NAME/environment.tfstate"
  (cd "$ENV_DIR" && eval "$TF init -input=false -reconfigure")

  # Destroying the app stack needs its generated config, not just its state:
  # Terraform cannot configure the provider from an empty directory. If the
  # manifest is missing, recompile it with the same compose file and project
  # name first; the environment teardown below still runs either way.
  if [[ -f "$BUILD_DIR/main.tf.json" ]]; then
    write_backend "$BUILD_DIR" "acceptance/$NAME/$PROJECT.tfstate"
    (cd "$BUILD_DIR" && eval "$TF init -input=false -reconfigure")
  else
    rm -rf "$BUILD_DIR"   # nothing to destroy from; skip it in cleanup
    echo "  NOTE: no $BUILD_DIR/main.tf.json — skipping the app stack."
    echo "        To destroy it too, first run:"
    echo "          $CLOUDCOMPOSE compile -f <copy of \$COMPOSE under build/> -e $ENV_DIR -p $PROJECT"
  fi

  exit 0   # the EXIT trap does the actual destroying
fi

# --- 0. Build the Go binary ---------------------------------------------------
# Built fresh rather than trusted from a stale copy on PATH: this script
# exists to catch real regressions, and a stale binary would defeat that
# the moment cloudcompose's source changed underneath it.
log "Building cloudcompose…"
(cd "$ROOT/cloudcompose-go" && go build -o cloudcompose ./cmd/cloudcompose)

# --- 1. Create environment with cloudcompose init ---------------------------------
log "Creating $PROVIDER environment '$NAME' with cloudcompose init…"
rm -rf "$ENV_DIR"
cd "$ROOT"

# cloudcompose init takes no decision flags -- environment.yaml is its only
# input (see docs/authored-environment-config.md). name: and region: are
# the fields this script can't commit statically: name: needs a unique
# resource prefix per run, and region: is itself a workflow input for
# both clouds (region-restricted managed-service capacity has caused a
# real failure on Azure before -- see the REGION comment above; AWS's
# own region input has no equivalent known restriction, but the same
# templating mechanism covers it for parity). Every other decision comes
# from the committed scripts/ci-environment.{aws,azure}.yaml, shared
# across every example this script deploys (they're separate apps
# sharing one platform environment -- the whole point of the init/compile
# split).
GENERATED_ENV_CONFIG="$ENV_CONFIG_DIR/$NAME-environment-$PROVIDER.yaml"
mkdir -p "$(dirname "$GENERATED_ENV_CONFIG")"
python3 -c "
with open('$ROOT/scripts/ci-environment.$PROVIDER.yaml') as f:
    content = f.read()
content = content.replace('name: PLACEHOLDER', 'name: $NAME')
if '$PROVIDER' == 'azure':
    content = content.replace('region: francecentral', 'region: $REGION')
else:
    content = content.replace('region: eu-west-2', 'region: $REGION')
with open('$GENERATED_ENV_CONFIG', 'w') as f:
    f.write(content)
"
"$CLOUDCOMPOSE" init -e "$GENERATED_ENV_CONFIG"

write_backend "$ENV_DIR" "acceptance/$NAME/environment.tfstate"
cd "$ENV_DIR"
eval "$TF init -input=false -reconfigure"
eval "$TF apply -auto-approve"

# --- Provider-specific: where the app will be reachable ----------------------
# AWS deploys behind a shared ALB (http, DNS name from the environment
# output); Azure gives each Container App its own FQDN (https, read from
# the app stack's own output once deployed below) -- genuinely different
# ingress models, not just a naming difference. AWS's URL is known as soon
# as the environment exists; Azure's only exists once the app itself is up.
if [[ "$PROVIDER" == "aws" ]]; then
  ALB_DNS="$(eval "$TF output -raw alb_dns_name")"
  [[ -n "$ALB_DNS" ]] || fail "environment produced no alb_dns_name"
  log "Environment up. ALB: $ALB_DNS"
else
  log "Environment up."
fi

# --- 2. Compile the app ------------------------------------------------------
log "Compiling $COMPOSE with cloudcompose…"
cd "$ROOT"
# compile has no output-location flag: it always writes to
# <dir of -f>/terraform (see docs/authored-environment-config.md's "no
# -o/--output flag" note). $COMPOSE's own directory (e.g. examples/doctor)
# is a committed example directory this script must not write build
# artifacts into, so the whole directory (compose file + any build
# context it references, e.g. a Dockerfile) is copied into
# $APP_BUILD_SRC first, and -f points at the copy there instead.
rm -rf "$APP_BUILD_SRC"
mkdir -p "$APP_BUILD_SRC"
cp -R "$ROOT/$(dirname "$COMPOSE")/." "$APP_BUILD_SRC/"
COMPOSE_BUILD_COPY="$APP_BUILD_SRC/$(basename "$COMPOSE")"
# --subnet-index not passed: defaults to 0, correct here since exactly
# one example ever deploys per CI run's environment (see
# examples/README.md's own note on this). A future run deploying more
# than one app into the same environment would need a distinct
# --subnet-index per app -- see docs/azure-app-isolation-design.md.
"$CLOUDCOMPOSE" compile -f "$COMPOSE_BUILD_COPY" -e "$ENV_DIR" -p "$PROJECT"

# --- 3. Deploy the app -------------------------------------------------------
log "Deploying app '$PROJECT'…"
write_backend "$BUILD_DIR" "acceptance/$NAME/$PROJECT.tfstate"
cd "$BUILD_DIR"
eval "$TF init -input=false -reconfigure"

if [[ "$PROVIDER" == "azure" ]]; then
  # azurerm_cdn_frontdoor_origin is created with enabled=false regardless of
  # what is configured — a known, unresolved provider bug (confirmed against
  # real Azure 2026-08-05: hashicorp/terraform-provider-azurerm#31647). Any
  # azurerm_cdn_frontdoor_route depending on that origin fails its first apply
  # with "Please make sure that the originGroup is created successfully and at
  # least one enabled origin is created under the origin group." A second
  # apply always succeeds: Terraform detects the drift on refresh and flips
  # the origin to enabled before the route is attempted again.
  #
  # A -target'd retry was tried first, to avoid touching anything else on the
  # second apply — but -target pulls in the whole dependency chain behind the
  # route (origin -> Container App -> Postgres connection string), so it
  # still re-touched azurerm_postgresql_flexible_server and hit a second bug:
  # Azure assigns that resource's `zone` itself, and any plan that reaches it
  # tries to "correct" that real value, which the API rejects
  # (models/azure.py's PostgreSQLFlexibleServer now carries
  # `lifecycle.ignore_changes: ["zone"]` so that no longer happens on *any*
  # plan, not just this retry). With that fixed at the source, retrying the
  # whole apply is simple and no longer trades one bug for another.
  if ! eval "$TF apply -auto-approve" 2>&1 | tee /tmp/tf-apply-app.log; then
    if grep -q "at least one enabled origin is created under the origin group" /tmp/tf-apply-app.log; then
      log "Front Door origin race hit (known azurerm provider bug #31647) — retrying apply…"
      eval "$TF apply -auto-approve"
    elif grep -q "ForbiddenByRbac" /tmp/tf-apply-app.log; then
      # Root cause (found 2026-08-12, after four consecutive real-Azure
      # failures survived widening a blind apply-retry loop rather than
      # converging on a bound): the GetSecret 403 in every failure names
      # the CI service principal itself as the denied caller
      # (`appid=<AZURE_CLIENT_ID>`), not the app's own managed identity —
      # `azurerm_role_assignment.kv_role` (Key Vault Secrets User) only
      # ever grants the *app's* identity read access; it was never meant
      # to, and never did, cover the identity actually running
      # `terraform apply` (Terraform itself creates azurerm_key_vault_secret
      # resources, a data-plane write/read, as the CI service principal).
      # Contributor's own `dataActions` is empty (confirmed against the
      # real role definition) -- it grants management-plane access only,
      # so the CI service principal had no path to Key Vault's data plane
      # at all, permanently, no matter how long anything waited. Fixed by
      # granting "Key Vault Secrets Officer" to the CI service principal
      # itself (ci/README.md's Azure setup section) -- a one-time,
      # subscription-level setup step, not something this script can grant
      # itself at runtime.
      #
      # This was misdiagnosed for a long time as pure RBAC propagation
      # delay (docs/azure-todo.md's Key Vault RBAC item): real propagation
      # delay for azurerm_role_assignment.kv_role does also exist on top of
      # this (Microsoft's own docs cite up to 10 minutes), which is why a
      # `time_sleep` + retry genuinely helped some runs without fully
      # fixing the underlying gap. The polling loop below stays as a
      # legitimate defense against *that* genuine, bounded propagation
      # delay -- now polling the Key Vault's own data plane directly
      # rather than retrying a full `apply` on every attempt, which is
      # cheap enough to run every 15s rather than 60-90s, and decoupled
      # from `apply`'s own replan/refresh cost entirely.
      KV_NAME="$(eval "$TF output -raw key_vault_name" 2>/dev/null || true)"
      [[ -n "$KV_NAME" ]] || fail "terraform apply failed for the app stack with ForbiddenByRbac, but no key_vault_name output to poll against"
      log "Key Vault RBAC propagation not yet visible (docs/azure-todo.md, ci/README.md) — polling $KV_NAME's data plane directly (up to 600s) instead of blind apply retries…"
      kv_deadline=$(( SECONDS + 600 ))
      kv_visible=0
      while (( SECONDS < kv_deadline )); do
        if az keyvault secret list --vault-name "$KV_NAME" --auth-mode login -o none 2>/dev/null; then
          kv_visible=1
          break
        fi
        printf '.'
        sleep 15
      done
      echo
      (( kv_visible == 1 )) || fail "Key Vault RBAC propagation still not visible on $KV_NAME's own data plane after 600s -- if this is a fresh CI service principal, check it has 'Key Vault Secrets Officer' granted per ci/README.md, not just propagation delay"
      log "Key Vault RBAC now visible on $KV_NAME's data plane — retrying apply once…"
      eval "$TF apply -auto-approve"
    else
      fail "terraform apply failed for the app stack"
    fi
  fi
  # `terraform output -raw` prints its "No outputs found" warning on stdout, so
  # a missing output is captured as the hostname rather than as an empty
  # string. Insist on a value that looks like a hostname instead.
  CONTAINER_APP_FQDN="$(eval "$TF output -raw fqdn" 2>/dev/null || true)"
  [[ "$CONTAINER_APP_FQDN" =~ ^[A-Za-z0-9.-]+$ ]] \
    || fail "app stack published no usable fqdn output (got: ${CONTAINER_APP_FQDN:-<empty>})"
  log "Container App FQDN: $CONTAINER_APP_FQDN"
  url="https://$CONTAINER_APP_FQDN$HTTP_PATH"

  # cdn_fqdn is only published when a service has cdn:true (see
  # azureCdnFQDN's own doc comment in generator.go) -- empty for most
  # examples, so no error if the output is absent, unlike CONTAINER_APP_FQDN
  # above.
  CDN_FQDN="$(eval "$TF output -raw cdn_fqdn" 2>/dev/null || true)"
  [[ "$CDN_FQDN" =~ ^[A-Za-z0-9.-]+$ ]] || CDN_FQDN=""
else
  eval "$TF apply -auto-approve"
  url="http://$ALB_DNS$HTTP_PATH"
fi

# --- 4. Poll the app's URL until it serves the page --------------------------
# Factored into a function so the Front Door poll below (step 4b) can
# reuse the exact same wait/retry shape rather than duplicating it.
poll_until_served() {
  local poll_url="$1" timeout="$2"
  log "Polling $poll_url (up to ${timeout}s)…"
  local deadline=$(( SECONDS + timeout ))
  local served=0
  body=""
  while (( SECONDS < deadline )); do
    # No -f: capture non-2xx bodies (e.g. a 503 /health) for the EXPECT
    # match and for diagnostics on timeout.
    body="$(curl -sS --max-time 5 "$poll_url" 2>/dev/null || true)"
    if [[ "$body" == *"$EXPECT"* ]]; then
      served=1
      break
    fi
    printf '.'
    sleep 5
  done
  return $(( served == 1 ? 0 : 1 ))
}

# show_diagnostics prints `cloudcompose ps`/`logs` output for the app just
# deployed -- live cloud status and recent stdout/stderr, queried directly
# rather than inferred from an HTTP response or Terraform state (see
# internal/compiler/{aws,azure}/status.go and logs.go's own doc comments).
# Purely diagnostic: poll_until_served above remains the actual pass/fail
# signal (it alone proves routing+TLS+the app's own response end to end);
# this only helps explain *why* a poll failed, or gives a live snapshot
# right after deploy, without ever gating success on what it prints.
# Failures from ps/logs themselves are swallowed (|| true) -- a transient
# API hiccup while gathering diagnostics must never mask the real
# poll_until_served failure this is trying to help explain.
#
# Uses $COMPOSE_BUILD_COPY, not $COMPOSE: by this point the script has
# already `cd`ed into $BUILD_DIR (see step 3, "Deploy the app," above)
# and never cds back, so $COMPOSE's own relative path (e.g.
# examples/hello/compose.yml, relative to $ROOT) no longer resolves --
# $COMPOSE_BUILD_COPY is the absolute path to the same file's copy under
# build/ that `compile` itself was given. A real bug found in CI: this
# used to pass $COMPOSE here, and every ps/logs call below failed with
# "does not exist or is not readable" -- silently for these
# diagnostics-only calls (|| true), loudly once the strict assertions
# below were added.
show_diagnostics() {
  local label="$1"
  log "cloudcompose ps ($label)…"
  "$CLOUDCOMPOSE" ps -f "$COMPOSE_BUILD_COPY" -e "$ENV_DIR" -p "$PROJECT" || true
  log "cloudcompose logs, last 5m ($label)…"
  "$CLOUDCOMPOSE" logs -f "$COMPOSE_BUILD_COPY" -e "$ENV_DIR" -p "$PROJECT" --since 5m --tail 200 || true
}

show_diagnostics "just deployed"

if ! poll_until_served "$url" "$POLL_TIMEOUT"; then
  echo
  echo "----- last response (for diagnosis) -----"
  echo "${body:-<no response>}" | head -20
  show_diagnostics "poll timed out"
  fail "timed out after ${POLL_TIMEOUT}s waiting for '$EXPECT' at $url"
fi

log "App is live — response contains '$EXPECT'. 🎉"
echo "----- response -----"
echo "$body" | head -20

# --- 4a. Assert cloudcompose ps/logs themselves work against the real cloud --
# Everything above (poll_until_served, show_diagnostics) treats ps/logs as
# pure diagnostics -- their output is printed but never checked, so a bug
# in either command could silently print garbage or nothing and this
# script would still report SUCCESS off the HTTP poll alone. These two
# checks turn them into something actually under test, using --json output
# (a stable, cloud-agnostic shape -- see cmd/cloudcompose/ps.go's own
# psRowJSON/logEventJSON) rather than grepping the human-readable table/log
# lines, which differ in column layout between AWS and Azure and could
# reasonably change formatting over time without this script caring.
#
# Deliberately NOT a replacement for poll_until_served: an HTTP response
# already proved routing+TLS+the app's own response end-to-end; ps/logs
# reporting correctly is an independent, additional thing worth knowing
# actually works, not a faster or more thorough substitute for the poll.
#
# Uses $COMPOSE_BUILD_COPY, not $COMPOSE -- see show_diagnostics' own
# comment above for why.
log "Asserting cloudcompose ps reports the deployed service as running…"
# A single shot here raced a real AWS eventual-consistency gap and failed
# CI (2026-08-16): RunningCount (from ECS's own DescribeServices) and
# target health (from a separate ELB DescribeTargetHealth call, see
# aws/status.go's targetHealth) are two independent APIs that don't
# necessarily agree the instant a task transitions to RUNNING -- the ALB
# had already reported the target healthy, and the HTTP poll above had
# already gotten a real response through it, while ECS's own
# RunningCount still read 0. Retry rather than a single shot, bounded
# rather than open-ended, mirroring the logs assertion below --
# PS_ASSERT_TIMEOUT defaults to 60s: this convergence gap is normally a
# few seconds on AWS, nowhere near log ingestion's own multi-minute
# ceiling, so a much shorter budget than LOGS_ASSERT_TIMEOUT is
# deliberate, not copied from it verbatim.
PS_ASSERT_TIMEOUT="${PS_ASSERT_TIMEOUT:-60}"
ps_deadline=$(( SECONDS + PS_ASSERT_TIMEOUT ))
ps_ok=0
while (( SECONDS < ps_deadline )); do
  if "$CLOUDCOMPOSE" ps -f "$COMPOSE_BUILD_COPY" -e "$ENV_DIR" -p "$PROJECT" --json | python3 -c "
import json, sys
rows = json.load(sys.stdin)
if not rows:
    sys.exit('ps --json returned no rows at all -- expected at least one compose service')
bad = [r for r in rows if not r.get('found') or r.get('running', 0) <= 0]
if bad:
    sys.exit(f'ps reports service(s) not running: {bad}')
print(f'ps OK -- {len(rows)} service(s) found and running: ' + ', '.join(r[\"name\"] for r in rows))
"; then
    ps_ok=1
    break
  fi
  printf '.'
  sleep 5
done
echo
(( ps_ok == 1 )) || fail "cloudcompose ps did not report the deployed service as running after ${PS_ASSERT_TIMEOUT}s (RunningCount/target-health convergence delay, or a real regression -- check the diagnostics above)"

log "Asserting cloudcompose logs returns real output…"
# Log ingestion is not instant (CloudWatch typically has single-digit
# seconds of delay; Azure Log Analytics' own ingestion latency can run
# into minutes -- Microsoft's own guidance is "usually under 5 minutes,
# occasionally longer"), so a single query run the instant the HTTP poll
# above succeeds can genuinely see zero lines even though the app has
# been logging the whole time it served that poll. Retry rather than a
# single shot, bounded rather than open-ended: LOGS_ASSERT_TIMEOUT
# defaults to 300s, comfortably inside Microsoft's own "occasionally
# longer" ceiling without being open-ended like FRONTDOOR_POLL_TIMEOUT
# above needs to be.
LOGS_ASSERT_TIMEOUT="${LOGS_ASSERT_TIMEOUT:-300}"
logs_deadline=$(( SECONDS + LOGS_ASSERT_TIMEOUT ))
logs_ok=0
while (( SECONDS < logs_deadline )); do
  if "$CLOUDCOMPOSE" logs -f "$COMPOSE_BUILD_COPY" -e "$ENV_DIR" -p "$PROJECT" --since 5m --tail 200 --json | python3 -c "
import json, sys
events = json.load(sys.stdin)
if not events:
    sys.exit(1)
print(f'logs OK -- {len(events)} line(s) returned')
"; then
    logs_ok=1
    break
  fi
  printf '.'
  sleep 10
done
echo
(( logs_ok == 1 )) || fail "cloudcompose logs returned no output for the deployed service after ${LOGS_ASSERT_TIMEOUT}s (log ingestion delay, or a real regression -- check the diagnostics above)"

# --- 4b. Front Door: confirm traffic actually flows through the CDN itself ---
# docs/azure-todo.md's Front Door item: a clean `terraform apply` only ever
# proved the five Front Door resources exist and reference each other
# correctly, never that Front Door actually proxies real traffic to the
# Container App end to end. cdn_fqdn (see azureCdnFQDN in generator.go) is
# only published when a service has cdn:true, so this step is a no-op for
# every other example. The Container App's own FQDN above already proved
# the app itself serves correctly; this proves the separate CDN/WAF hop in
# front of it also works, not just that Terraform thinks it applied cleanly.
if [[ -n "${CDN_FQDN:-}" ]]; then
  cdn_url="https://$CDN_FQDN$HTTP_PATH"
  # Front Door's own DNS + edge propagation is a separate, additional delay
  # on top of the Container App's own cold start already waited out above,
  # so this gets its own timeout rather than reusing whatever budget the
  # first poll had left. Confirmed against a real run (2026-08-12,
  # production-stack/francecentral, after fixing the unrelated Key Vault
  # RBAC data-plane permission gap that had blocked every previous attempt
  # at reaching this step at all): the route itself created successfully
  # and the Container App's own FQDN served correctly, but Front Door's
  # own endpoint still returned "Page not found" (Front Door's own error
  # page, not a timeout/connection error) after the full 480s POLL_TIMEOUT
  # — global anycast edge propagation for a newly created route can
  # genuinely take longer than that. FRONTDOOR_POLL_TIMEOUT defaults to
  # 900s (Microsoft's own guidance for Front Door propagation is "a few
  # minutes up to 10"), independent of POLL_TIMEOUT so a slow-to-serve
  # Container App doesn't need every other example to also wait longer.
  if ! poll_until_served "$cdn_url" "$FRONTDOOR_POLL_TIMEOUT"; then
    echo
    echo "----- last response (for diagnosis) -----"
    echo "${body:-<no response>}" | head -20
    show_diagnostics "Front Door poll timed out"
    fail "timed out after ${FRONTDOOR_POLL_TIMEOUT}s waiting for '$EXPECT' through Front Door at $cdn_url"
  fi
  log "Front Door is live — response contains '$EXPECT' through the CDN endpoint too. 🎉"
fi

# --- 5. Managed-resource assertions ------------------------------------------
# Prove every substituted service (minio->S3/Blob, postgres->RDS/Flexible
# Server, redis->ElastiCache/Managed Redis) really landed in the target cloud
# and that endpoint injection reached the deployed task. Driven off applied
# TF state; a no-op for examples with nothing substituted.
log "Asserting managed substitutions against applied $PROVIDER state…"
( cd "$BUILD_DIR" && eval "$TF show -json" ) | python3 "$ROOT/scripts/assert_managed.py" \
  || fail "managed substitution assertions failed"

log "SUCCESS — everything verified on real ${PROVIDER}."
exit 0
