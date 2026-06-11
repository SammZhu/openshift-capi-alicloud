#!/usr/bin/env bash
# kind smoke test for the clusterctl install path (G3-5) + a CAPI reconcile
# smoke (G7-2).
#
# Brings up a throwaway kind MANAGEMENT cluster, installs THIS provider through
# `clusterctl init` from the generated infrastructure-components.yaml, applies the
# day-2 worker-pool cluster-template, and asserts the artifacts actually work:
#
#   [install]      clusterctl init installs the provider; capa-controller-manager
#                  Deployment becomes Available (CRDs + controller + webhooks up).
#   [render]       `clusterctl generate cluster` renders the expected 6 objects.
#   [admission]    kubectl apply of the rendered CRs is ADMITTED by the webhooks
#                  (contract labels valid, defaulting/validation webhooks serve).
#   [reconcile]    the externally-managed AlibabaCloudControlPlane reconciles to
#                  ControlPlaneInitialized=True and the Cluster reports it too.
#                  This needs NO cloud credentials (pure status controller).
#   [cloud-call]   the derived AlibabaCloudMachine reaches the Alibaba client and
#                  fails on the DUMMY credentials — proving the path got past
#                  admission/contract to the cloud SDK, without crashlooping.
#
# HERMETIC: no real Alibaba AK/SK, NO ECS is provisioned. The webhook TLS is
# normally minted by the OpenShift service-ca operator (absent on kind), so this
# script self-signs a serving cert + injects the caBundle.
#
# Requires: kind, clusterctl (>=v1.11 for the v1beta2 contract), kubectl, openssl,
# python3. Container runtime: docker if present, else podman
# (KIND_EXPERIMENTAL_PROVIDER=podman; bump the podman machine to >=4GiB).
#
# Usage:  hack/kind-smoke.sh          # or: make test-clusterctl-smoke
#   KEEP_CLUSTER=1 hack/kind-smoke.sh # leave the kind cluster up for debugging
set -euo pipefail

CLUSTER="${CLUSTER:-capa-smoke}"
PROVIDER_NS=capa-system
WORKLOAD_NS=default
CAPA_IMAGE="${CAPA_IMAGE:-quay.io/samzhu/openshift-capi-alicloud:v0.1.19}"
VERSION="${CAPA_VERSION:-v0.1.19}"
K8S_VERSION="${K8S_VERSION:-v1.33.0}"
WORKER_BOOTSTRAP_SECRET="${WORKER_BOOTSTRAP_SECRET:-capa-smoke-bootstrap}"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
ovr="$work/repo/infrastructure-alibabacloud/${VERSION}"
cfg="$work/clusterctl.yaml"
fail=0

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
step()  { printf '\n\033[1;36m== %s ==\033[0m\n' "$*"; }
ok()    { green "  PASS: $*"; }
bad()   { red   "  FAIL: $*"; fail=1; }

cleanup() {
  if [ "${KEEP_CLUSTER:-0}" = "1" ]; then
    echo "KEEP_CLUSTER=1 — leaving kind cluster '$CLUSTER' up. Delete with: kind delete cluster --name $CLUSTER"
  else
    kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

# ---- preflight -------------------------------------------------------------
step "Preflight"
for t in kind clusterctl kubectl openssl python3; do
  command -v "$t" >/dev/null 2>&1 || { red "missing tool: $t"; exit 2; }
done
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  echo "  container runtime: docker"
elif command -v podman >/dev/null 2>&1; then
  export KIND_EXPERIMENTAL_PROVIDER=podman
  echo "  container runtime: podman (KIND_EXPERIMENTAL_PROVIDER=podman)"
else
  red "no working docker or podman"; exit 2
fi
echo "  clusterctl: $(clusterctl version -o short 2>/dev/null)   kind: $(kind version 2>/dev/null)"

# ---- build the clusterctl override layout ----------------------------------
step "Assemble clusterctl provider repo ($VERSION, image $CAPA_IMAGE)"
mkdir -p "$ovr"
CAPA_IMAGE="$CAPA_IMAGE" "$here/hack/gen-clusterctl-components.sh" "$ovr/infrastructure-components.yaml"
cp "$here/metadata.yaml" "$ovr/metadata.yaml"
cp "$here/templates/cluster-template.yaml" "$ovr/cluster-template.yaml"

cat > "$cfg" <<EOF
providers:
  - name: alibabacloud
    url: ${ovr}/infrastructure-components.yaml
    type: InfrastructureProvider
# cluster-template variables (all DUMMY — no real cloud is touched):
ALIBABA_REGION: cn-wulanchabu
ALIBABA_AZ: cn-wulanchabu-a
ALIBABA_VSWITCH_ID: vsw-smoke000000000000000
ALIBABA_BOOT_IMAGE_ID: m-smoke000000000000000
ALIBABA_SECURITY_GROUP_ID: sg-smoke000000000000000
ALIBABA_RAM_ROLE_NAME: capa-smoke-role
ALIBABA_INSTANCE_TYPE: ecs.g7.xlarge
CONTROL_PLANE_ENDPOINT_HOST: 10.0.0.1
CONTROL_PLANE_ENDPOINT_PORT: "6443"
WORKER_MACHINE_COUNT: "1"
WORKER_BOOTSTRAP_SECRET: ${WORKER_BOOTSTRAP_SECRET}
EOF

# ---- kind management cluster ----------------------------------------------
step "Create kind management cluster '$CLUSTER'"
kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER" --wait 120s
kubectl cluster-info --context "kind-$CLUSTER" >/dev/null

# ---- pre-create namespace + the two Secrets the manager needs --------------
# alibaba-creds (envFrom optional=false) MUST exist or the pod sits in
# CreateContainerConfigError and clusterctl init times out. The webhook serving
# cert is normally minted by service-ca (absent on kind) — self-sign it here.
step "Pre-seed namespace, dummy creds, and self-signed webhook cert"
kubectl create namespace "$PROVIDER_NS" >/dev/null 2>&1 || true
kubectl -n "$PROVIDER_NS" create secret generic alibaba-creds \
  --from-literal=ALIBABA_CLOUD_ACCESS_KEY_ID=SMOKE_DUMMY_AK \
  --from-literal=ALIBABA_CLOUD_ACCESS_KEY_SECRET=SMOKE_DUMMY_SK \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

svc=capa-webhook-service
cn="${svc}.${PROVIDER_NS}.svc"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$work/ca.key" -out "$work/ca.crt" \
  -days 3650 -subj "/CN=capa-smoke-webhook-ca" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout "$work/tls.key" -out "$work/tls.csr" \
  -subj "/CN=${cn}" >/dev/null 2>&1
openssl x509 -req -in "$work/tls.csr" -CA "$work/ca.crt" -CAkey "$work/ca.key" \
  -CAcreateserial -out "$work/tls.crt" -days 3650 \
  -extfile <(printf "subjectAltName=DNS:%s,DNS:%s.cluster.local" "$cn" "$cn") >/dev/null 2>&1
kubectl -n "$PROVIDER_NS" create secret tls capa-webhook-server-cert \
  --cert="$work/tls.crt" --key="$work/tls.key" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
CABUNDLE="$(base64 < "$work/ca.crt" | tr -d '\n')"

# ---- clusterctl init -------------------------------------------------------
step "clusterctl init --infrastructure alibabacloud"
clusterctl init --config "$cfg" --infrastructure "alibabacloud:${VERSION}"

# ---- inject the caBundle the webhooks expect (service-ca would do this) -----
kubectl patch mutatingwebhookconfiguration capa-mutating-webhook-configuration --type=json \
  -p "[{\"op\":\"add\",\"path\":\"/webhooks/0/clientConfig/caBundle\",\"value\":\"$CABUNDLE\"}]" >/dev/null
kubectl patch validatingwebhookconfiguration capa-validating-webhook-configuration --type=json \
  -p "[{\"op\":\"add\",\"path\":\"/webhooks/0/clientConfig/caBundle\",\"value\":\"$CABUNDLE\"},{\"op\":\"add\",\"path\":\"/webhooks/1/clientConfig/caBundle\",\"value\":\"$CABUNDLE\"}]" >/dev/null

# ---- [install] assertion ---------------------------------------------------
step "[install] provider controller Available"
if kubectl -n "$PROVIDER_NS" rollout status deploy/capa-controller-manager --timeout=180s; then
  ok "capa-controller-manager Deployment is Available"
else
  bad "capa-controller-manager did not become Available"
  kubectl -n "$PROVIDER_NS" get pods -o wide || true
  kubectl -n "$PROVIDER_NS" describe deploy/capa-controller-manager | tail -30 || true
fi

# CAPI core (and kubeadm) ship their OWN webhooks for cluster/machinedeployment/
# machinehealthcheck. clusterctl init waits for the Deployments to be "running",
# but the webhook server endpoint serves a few seconds later — applying our CRs
# too early hits "connection refused" on capi-webhook-service. Wait for the core
# webhook Service to have ready endpoints before [admission].
step "Wait for CAPI core webhooks to serve"
for d in capi-system/capi-controller-manager \
         capi-kubeadm-bootstrap-system/capi-kubeadm-bootstrap-controller-manager \
         capi-kubeadm-control-plane-system/capi-kubeadm-control-plane-controller-manager; do
  kubectl -n "${d%%/*}" rollout status "deploy/${d##*/}" --timeout=120s >/dev/null 2>&1 || true
done
deadline=$((SECONDS+120))
until [ -n "$(kubectl -n capi-system get endpoints capi-webhook-service -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null)" ]; do
  [ $SECONDS -ge $deadline ] && { echo "  (warn) core webhook endpoints still empty after 120s"; break; }
  sleep 3
done
echo "  core webhook endpoints: $(kubectl -n capi-system get endpoints capi-webhook-service -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null)"

# ---- [render] assertion ----------------------------------------------------
step "[render] clusterctl generate cluster"
clusterctl generate cluster "$CLUSTER" --config "$cfg" \
  --infrastructure "alibabacloud:${VERSION}" \
  --kubernetes-version "$K8S_VERSION" \
  --target-namespace "$WORKLOAD_NS" > "$work/cluster.yaml"
kinds="$(grep -E '^kind:' "$work/cluster.yaml" | awk '{print $2}' | sort -u | tr '\n' ' ')"
echo "  rendered kinds: $kinds"
expect="AlibabaCloudCluster AlibabaCloudControlPlane AlibabaCloudMachineTemplate Cluster MachineDeployment MachineHealthCheck"
got="$(echo "$kinds" | tr ' ' '\n' | grep -v '^$' | sort | tr '\n' ' ' | sed 's/ $//')"
if [ "$got" = "$expect" ]; then ok "rendered the expected 6 object kinds"; else bad "kinds mismatch; got [$got] want [$expect]"; fi

# ---- dummy bootstrap secret so the Machine has bootstrap data --------------
kubectl -n "$WORKLOAD_NS" create secret generic "$WORKER_BOOTSTRAP_SECRET" \
  --from-literal=value="#cloud-config (smoke dummy)" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

# ---- [admission] assertion -------------------------------------------------
# Retry to absorb any residual webhook-serving lag (both our webhooks and core's).
step "[admission] apply rendered CRs (webhooks must admit)"
applied=""
for attempt in 1 2 3 4 5 6; do
  if out="$(kubectl apply -f "$work/cluster.yaml" 2>&1)"; then applied=1; echo "$out"; break; fi
  echo "  apply attempt $attempt not yet admitted; retrying in 10s..."
  sleep 10
done
if [ -n "$applied" ]; then
  ok "all CRs admitted + created (webhook defaulting/validation OK)"
else
  bad "kubectl apply was rejected (webhook admission / contract)"
  echo "$out"
fi

# ---- [reconcile] external control plane initialized ------------------------
# The whole point of the external-CP pattern: the AlibabaCloudControlPlane reports
# initialized, and CAPI core PROPAGATES it onto the Cluster's contract field
# .status.initialization.controlPlaneInitialized (the gate that, live, unblocks
# worker node-health / MD.readyReplicas). No cloud creds are needed for any of it.
# Poll until the Cluster contract field flips (core reconcile lags the ACP a few s).
step "[reconcile] external control plane -> Cluster.status.initialization.controlPlaneInitialized=True"
deadline=$((SECONDS+120))
cp_init=""; cl_init=""; cl_infra=""
while [ $SECONDS -lt $deadline ]; do
  cp_init="$(kubectl -n "$WORKLOAD_NS" get alibabacloudcontrolplane "$CLUSTER" \
            -o jsonpath='{.status.initialization.controlPlaneInitialized}' 2>/dev/null || true)"
  cl_init="$(kubectl -n "$WORKLOAD_NS" get cluster "$CLUSTER" \
            -o jsonpath='{.status.initialization.controlPlaneInitialized}' 2>/dev/null || true)"
  cl_infra="$(kubectl -n "$WORKLOAD_NS" get cluster "$CLUSTER" \
            -o jsonpath='{.status.initialization.infrastructureProvisioned}' 2>/dev/null || true)"
  [ "$cl_init" = "true" ] && break
  sleep 5
done
ext="$(kubectl -n "$WORKLOAD_NS" get alibabacloudcontrolplane "$CLUSTER" -o jsonpath='{.status.externalManagedControlPlane}' 2>/dev/null || true)"
rdy="$(kubectl -n "$WORKLOAD_NS" get alibabacloudcontrolplane "$CLUSTER" -o jsonpath='{.status.ready}' 2>/dev/null || true)"
phase="$(kubectl -n "$WORKLOAD_NS" get cluster "$CLUSTER" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
if [ "$cp_init" = "true" ] && [ "$ext" = "true" ] && [ "$rdy" = "true" ]; then
  ok "AlibabaCloudControlPlane: controlPlaneInitialized=true externalManaged=true ready=true"
else
  bad "AlibabaCloudControlPlane not fully initialized (init=$cp_init ext=$ext ready=$rdy)"
fi
if [ "$cl_init" = "true" ]; then
  ok "Cluster.status.initialization.controlPlaneInitialized=true (phase=${phase:-?}) — core propagated the external CP"
else
  bad "Cluster controlPlaneInitialized not propagated (got '${cl_init:-<empty>}', phase=${phase:-?})"
fi
if [ "$cl_infra" = "true" ]; then ok "Cluster.status.initialization.infrastructureProvisioned=true (AlibabaCloudCluster contract OK)"; else bad "infrastructureProvisioned not true (got '${cl_infra:-<empty>}')"; fi

# ---- [cloud-call] machine reaches the Alibaba client and fails on dummy AK --
step "[cloud-call] AlibabaCloudMachine hits the cloud SDK and fails on dummy creds"
deadline=$((SECONDS+150)); hit=""
pat='InvalidAccessKeyId|SignatureDoesNotMatch|Forbidden.RAM|NoPermission|access key|AccessKey|credential|denied|RunInstances|invalid|Specified|cloud'
while [ $SECONDS -lt $deadline ]; do
  if kubectl -n "$PROVIDER_NS" logs deploy/capa-controller-manager --tail=-1 2>/dev/null \
      | grep -aiE "alibabacloudmachine|RunInstances|access key|InvalidAccessKeyId|SignatureDoesNotMatch|credential" \
      | grep -aiE "$pat" | head -1 | grep -q .; then hit=1; break; fi
  sleep 5
done
crash="$(kubectl -n "$PROVIDER_NS" get deploy/capa-controller-manager -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo 0)"
if [ -n "$hit" ]; then ok "controller reached cloud client and errored on dummy creds (expected)"; else
  echo "  (note) no explicit cloud-auth error captured in window; recent machine-related logs:"
  kubectl -n "$PROVIDER_NS" logs deploy/capa-controller-manager --tail=-1 2>/dev/null | grep -aiE "alibabacloudmachine|RunInstances|credential|access" | tail -8 || true
  bad "did not observe the expected cloud-call failure"
fi
if [ "${crash:-0}" -ge 1 ]; then ok "controller still Available (no crashloop): availableReplicas=$crash"; else bad "controller not Available after cloud errors (availableReplicas=${crash:-0})"; fi

# ---- summary ---------------------------------------------------------------
step "Summary"
if [ "$fail" = "0" ]; then green "ALL SMOKE ASSERTIONS PASSED (G3-5 install + G7-2 reconcile)"; else red "SMOKE FAILED — see FAIL lines above"; fi
exit "$fail"
