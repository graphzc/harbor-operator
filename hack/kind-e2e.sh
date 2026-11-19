#!/usr/bin/env bash
# End-to-end test against a real Harbor running in kind.
#
# Creates a kind cluster, installs Harbor with its official Helm chart,
# installs the operator with charts/harbor-operator, creates every resource
# kind, verifies the result through the Harbor API (including logging in with
# the generated credentials), rotates a secret, and checks cleanup on delete.
#
# Environment:
#   KIND_CLUSTER          kind cluster name (default harbor-operator-e2e)
#   KEEP_CLUSTER          "true" keeps the cluster afterwards (default false)
#   HARBOR_CHART_VERSION  goharbor/harbor chart version (default 1.19.4)
#   HARBOR_VERSION        Harbor image tag matching the chart (default v2.15.4)
#   LOCAL_PORT            local port forwarded to Harbor (default 18080)
set -euo pipefail

CLUSTER="${KIND_CLUSTER:-harbor-operator-e2e}"
CTX="kind-${CLUSTER}"
KEEP_CLUSTER="${KEEP_CLUSTER:-false}"
HARBOR_CHART_VERSION="${HARBOR_CHART_VERSION:-1.19.4}"
HARBOR_VERSION="${HARBOR_VERSION:-v2.15.4}"
LOCAL_PORT="${LOCAL_PORT:-18080}"
ADMIN_PASSWORD="Harbor12345"
IMG="harbor-operator:e2e"
NS="harbor-e2e"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HARBOR="http://localhost:${LOCAL_PORT}"
BODY="$(mktemp)"

# Always target the kind cluster explicitly, never the current context.
k() { kubectl --context "$CTX" "$@"; }
h() { helm --kube-context "$CTX" "$@"; }
log() { printf '\n==> %s\n' "$*"; }
pass() { printf '    ok: %s\n' "$*"; }
fail() { printf '    FAIL: %s\n' "$*" >&2; exit 1; }

# api USER:PASS PATH prints the HTTP status and body of a Harbor API GET.
api() { curl -s -u "$1" -o "$BODY" -w '%{http_code}' "${HARBOR}/api/v2.0$2"; }
body() { cat "$BODY"; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
admin_get() { [ "$(api "admin:${ADMIN_PASSWORD}" "$1")" = 200 ] || fail "GET $1"; body; }
secret_value() { k -n "$NS" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }

# token_actions USER:PASS SCOPE prints the actions the registry token grants.
token_actions() {
  curl -s -u "$1" "${HARBOR}/service/token?service=harbor-registry&scope=$2" | python3 -c '
import base64, json, sys
tok = json.load(sys.stdin).get("token", "")
if not tok:
    print(""); sys.exit()
payload = tok.split(".")[1]
claims = json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
print(",".join(sorted(a for acc in claims.get("access", []) for a in acc.get("actions", []))))'
}

# eventually CMD... retries CMD for up to 60 seconds.
eventually() {
  for _ in $(seq 60); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}

cleanup() {
  [ -n "${PF_PID:-}" ] && kill "$PF_PID" 2>/dev/null || true
  rm -f "$BODY"
  if [ "$KEEP_CLUSTER" != "true" ]; then
    log "Deleting kind cluster ${CLUSTER} (set KEEP_CLUSTER=true to keep it)"
    kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

for bin in kind kubectl helm docker curl python3; do
  command -v "$bin" >/dev/null || { echo "missing required tool: $bin" >&2; exit 1; }
done

log "Creating kind cluster ${CLUSTER}"
kind get clusters 2>/dev/null | grep -qx "$CLUSTER" || kind create cluster --name "$CLUSTER" --wait 120s

log "Loading Harbor ${HARBOR_VERSION} images"
# Harbor publishes linux/amd64 images only. Loading them into kind lets them
# run on arm64 hosts under Docker Desktop's emulation.
images=()
for img in nginx-photon harbor-core harbor-db harbor-jobservice harbor-portal \
  harbor-registryctl registry-photon valkey-photon; do
  ref="docker.io/goharbor/${img}:${HARBOR_VERSION}"
  docker pull -q --platform linux/amd64 "$ref" >/dev/null
  images+=("$ref")
done
kind load docker-image --name "$CLUSTER" "${images[@]}" >/dev/null

log "Installing Harbor (chart ${HARBOR_CHART_VERSION})"
helm repo list 2>/dev/null | grep -qE '^harbor[[:space:]]' || helm repo add harbor https://helm.goharbor.io >/dev/null
helm repo update harbor >/dev/null
h upgrade --install harbor harbor/harbor --version "$HARBOR_CHART_VERSION" \
  -n harbor --create-namespace \
  --set expose.type=clusterIP --set expose.tls.enabled=false \
  --set externalURL=http://harbor.harbor.svc.cluster.local \
  --set harborAdminPassword="$ADMIN_PASSWORD" \
  --set persistence.enabled=false --set trivy.enabled=false \
  --set imagePullPolicy=IfNotPresent \
  --wait --timeout 15m >/dev/null

log "Building and installing the operator with charts/harbor-operator"
docker build -q -t "$IMG" "$ROOT" >/dev/null
kind load docker-image --name "$CLUSTER" "$IMG" >/dev/null
h upgrade --install harbor-operator "$ROOT/charts/harbor-operator" \
  -n harbor-operator-system --create-namespace \
  --set image.repository="${IMG%%:*}" --set image.tag="${IMG##*:}" --set image.pullPolicy=Never \
  --set logging.encoder=console --set "watchNamespaces={${NS}}" \
  --wait --timeout 3m >/dev/null
# Restart so a rebuilt image with the same tag is picked up.
k -n harbor-operator-system rollout restart deploy/harbor-operator >/dev/null
k -n harbor-operator-system rollout status deploy/harbor-operator --timeout 120s >/dev/null
pass "operator running"

k -n harbor port-forward svc/harbor "${LOCAL_PORT}:80" >/dev/null 2>&1 &
PF_PID=$!
eventually curl -sf "${HARBOR}/api/v2.0/ping" || fail "Harbor not reachable on ${HARBOR}"

log "Creating Harbor resources from config/samples"
k create namespace "$NS" --dry-run=client -o yaml | k apply -f - >/dev/null
k -n "$NS" create secret generic harbor-admin --from-literal=password="$ADMIN_PASSWORD" \
  --dry-run=client -o yaml | k apply -f - >/dev/null
k -n "$NS" create secret generic puller-secret --from-literal=secret=PullerS3cretValue \
  --dry-run=client -o yaml | k apply -f - >/dev/null
sed 's#https://harbor.example.com#http://harbor.harbor.svc.cluster.local#' \
  "$ROOT/config/samples/harbor_v1alpha1_harborconnection.yaml" | k -n "$NS" apply -f - >/dev/null
for kind in user group project robotaccount configuration; do
  k -n "$NS" apply -f "$ROOT/config/samples/harbor_v1alpha1_${kind}.yaml" >/dev/null
done
k -n "$NS" wait --for=condition=Ready harborconnection/harbor --timeout=120s >/dev/null
k -n "$NS" wait --for=condition=Ready users,groups,projects,robotaccounts,configurations --all --timeout=180s >/dev/null
k -n "$NS" get harborconnections,users,groups,projects,robotaccounts,configurations
pass "all resources Ready"

log "Verifying Harbor state"
pid="$(k -n "$NS" get project team-a -o jsonpath='{.status.projectID}')"
gid="$(k -n "$NS" get group developers -o jsonpath='{.status.groupID}')"
[ -n "$pid" ] && [ -n "$gid" ] || fail "status IDs missing (project=$pid group=$gid)"
pass "status IDs recorded (project=$pid group=$gid)"

md="$(admin_get /projects/team-a | json 'sorted(d["metadata"].items())')"
for want in "('auto_scan', 'true')" "('prevent_vul', 'true')" "('severity', 'high')" "('public', 'false')"; do
  [[ "$md" == *"$want"* ]] || fail "project metadata missing $want: $md"
done
pass "project policies applied"

[ "$(admin_get /projects/team-a | json 'd["cve_allowlist"]["items"][0]["cve_id"]')" = CVE-2024-0001 ] || fail "CVE allowlist"
pass "CVE allowlist applied"

[ "$(admin_get "/quotas?reference=project&reference_id=${pid}" | json 'd[0]["hard"]["storage"]')" = 53687091200 ] || fail "quota"
pass "storage quota is 50Gi"

members="$(admin_get "/projects/${pid}/members" | json 'sorted((m["entity_name"], m["role_name"]) for m in d)')"
[ "$members" = "[('admin', 'projectAdmin'), ('alice', 'maintainer'), ('developers', 'developer')]" ] || fail "members: $members"
pass "members and roles: $members"

alice_pw="$(secret_value alice-credentials password)"
[ "$(api "alice:${alice_pw}" /users/current)" = 200 ] || fail "alice cannot log in with the generated password"
pass "user alice logs in with the generated password"

ci_user="$(secret_value team-a-ci-credentials username)"
ci_pw="$(secret_value team-a-ci-credentials password)"
[ "$(token_actions "${ci_user}:${ci_pw}" 'repository:team-a/app:pull,push')" = "pull,push" ] || fail "project robot token"
pass "project robot ${ci_user} gets pull,push on team-a"

[ "$(token_actions 'robot$puller:PullerS3cretValue' 'repository:team-a/app:pull,push')" = "pull" ] || fail "system robot token"
pass "system robot robot\$puller gets pull only, with the referenced secret"

cfg="$(admin_get /configurations | json '(d["project_creation_restriction"]["value"], d["robot_token_duration"]["value"])')"
[ "$cfg" = "('adminonly', 90)" ] || fail "configuration: $cfg"
pass "configuration applied: $cfg"

log "Rotating the system robot secret"
k -n "$NS" create secret generic puller-secret --from-literal=secret=PullerN3wSecretValue \
  --dry-run=client -o yaml | k apply -f - >/dev/null
new_works() { [ "$(token_actions 'robot$puller:PullerN3wSecretValue' 'repository:team-a/app:pull')" = pull ]; }
eventually new_works || fail "rotated secret not applied"
[ -z "$(token_actions 'robot$puller:PullerS3cretValue' 'repository:team-a/app:pull')" ] || fail "old secret still works"
pass "new secret works, old secret rejected"

log "Updating the project"
k -n "$NS" patch project team-a --type merge \
  -p '{"spec":{"public":true,"members":[{"kind":"User","name":"alice","role":"Developer"}]}}' >/dev/null
updated() {
  [ "$(admin_get /projects/team-a | json 'd["metadata"]["public"]')" = true ] &&
    [ "$(admin_get "/projects/${pid}/members" | json 'sorted((m["entity_name"], m["role_name"]) for m in d)')" = \
      "[('admin', 'projectAdmin'), ('alice', 'developer')]" ]
}
eventually updated || fail "project update not applied"
pass "project made public, group member removed, alice now developer"

log "Deleting resources"
uid="$(k -n "$NS" get user alice -o jsonpath='{.status.userID}')"
rid="$(k -n "$NS" get robotaccount team-a-ci -o jsonpath='{.status.robotID}')"
k -n "$NS" delete robotaccounts --all --wait --timeout 60s >/dev/null
k -n "$NS" delete projects,users,groups,configurations --all --wait --timeout 60s >/dev/null
for path in "/robots/${rid}" "/projects/${pid}" "/users/${uid}" "/usergroups/${gid}"; do
  [ "$(api "admin:${ADMIN_PASSWORD}" "$path")" = 404 ] || fail "$path still exists in Harbor"
done
pass "robot, project, user and group removed from Harbor"
[ "$(admin_get /configurations | json 'd["project_creation_restriction"]["value"]')" = adminonly ] || fail "configuration reset on delete"
pass "configuration left in place on delete"
secrets_gone() { ! k -n "$NS" get secret alice-credentials team-a-ci-credentials >/dev/null 2>&1; }
eventually secrets_gone || fail "generated Secrets not garbage collected"
pass "generated Secrets garbage collected"

log "PASS"
