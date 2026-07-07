# Quick Start

Get hands-on with the Alibaba Cloud infrastructure provider in the order that
wastes the least of your time:

1. **[See it work in ~5 minutes](#1-see-it-work-in-5-minutes-no-cloud-account)** — one command, on your laptop, **no Alibaba account and no ECS spend**.
2. **[Use it for real](#2-use-it-for-real)** — install it against a real management cluster and bring up worker machines.
3. **[Pick the right project](#3-which-project-do-i-need)** — this provider is one piece; the storage driver and the full OpenShift-on-Alibaba install live in sibling repos.

> **Unofficial, community-maintained.** Not affiliated with or endorsed by
> Alibaba Cloud, Red Hat, or OpenShift. Apache-2.0, provided as-is.

---

## 1. See it work in 5 minutes (no cloud account)

This spins up a throwaway [kind](https://kind.sigs.k8s.io/) cluster on your
machine, installs **this provider** through `clusterctl`, applies a day-2 worker
pool, and asserts the whole path actually works — install → render → webhook
admission → the external control plane reconciles → the machine reaches the
Alibaba SDK. **Hermetic: dummy credentials, nothing is provisioned in the cloud.**

**Prerequisites** (all `brew install …` on macOS): `kind`, `clusterctl` (≥ v1.11),
`kubectl`, `python3`, and a container runtime — Docker if present, otherwise
Podman (bump the Podman machine to ≥ 4 GiB).

```sh
make demo
```

You'll watch it print `PASS` for each stage and finish with
`ALL SMOKE ASSERTIONS PASSED`:

| Stage | What it proves |
|---|---|
| `[install]` | `clusterctl init` installs the provider; the controller Deployment goes Available (CRDs + controller + webhooks up). |
| `[render]` | `clusterctl generate cluster` renders the expected worker-pool objects. |
| `[admission]` | `kubectl apply` of the rendered CRs is admitted by the validating/defaulting webhooks. |
| `[reconcile]` | the externally-managed `AlibabaCloudControlPlane` reconciles to `controlPlaneInitialized=true` and CAPI core propagates it onto the `Cluster`. |
| `[cloud-call]` | the `AlibabaCloudMachine` reaches the Alibaba SDK and fails *cleanly* on the dummy credentials — proving the path got all the way to the cloud client without crash-looping. |

Want to poke at the cluster afterwards instead of tearing it down:

```sh
KEEP_CLUSTER=1 make demo
# ... explore ...
kind delete cluster --name capa-smoke
```

This is the same run as `make test-clusterctl-smoke` in CI — so a green `make
demo` is also your local signal that a build is healthy.

---

## 2. Use it for real

Once the demo makes sense, the full walkthrough — Alibaba prerequisites,
credentials, importing the RHCOS image, worker bootstrap data, verification, and
troubleshooting — is in **[GETTING-STARTED.md](GETTING-STARTED.md)**.

The condensed path:

```sh
# 1. Point clusterctl at the released provider artifacts and install it
#    (clusterctl pulls in cert-manager + CAPI core automatically):
cat >> ~/.cluster-api/clusterctl.yaml <<'EOF'
providers:
  - name: alibabacloud
    url: https://github.com/SammZhu/openshift-capi-alicloud/releases/latest/download/infrastructure-components.yaml
    type: InfrastructureProvider
EOF
clusterctl init --infrastructure alibabacloud

# 2. Give the controller Alibaba credentials (AccessKey Secret or ECS RAM role) —
#    see GETTING-STARTED.md § Authentication.

# 3. Render + apply a worker pool (fill in the FILL_IN placeholders first):
cp examples/capi-machinedeployment.yaml my-workers.yaml
$EDITOR my-workers.yaml
kubectl apply -f my-workers.yaml

# 4. Watch real ECS workers come up:
kubectl get alibabacloudmachines,machines -A
```

- **`clusterctl` details** (override layout, `clusterctl generate cluster`, the
  cert-manager vs OpenShift service-ca webhook paths): [CLUSTERCTL.md](CLUSTERCTL.md).
- **Full `AlibabaCloudMachineTemplate` field reference** and multi-AZ / spot /
  disk examples: [`examples/capi-machinedeployment.yaml`](../examples/capi-machinedeployment.yaml)
  and the [README](../README.md#alibabacloudmachinetemplate-spec-reference).

---

## 3. Which project do I need?

This provider is the **day-2 worker plane**. Depending on your goal you may want
a sibling project instead — or in addition:

| Your goal | Use | Quick demo |
|---|---|---|
| Add Alibaba ECS **worker machines** to an existing CAPI/OpenShift management cluster | **This repo** (`openshift-capi-alicloud`) | `make demo` (above) |
| Install a **whole OpenShift cluster on Alibaba Cloud** end-to-end (VPC/SLB, control plane via Assisted/Agent-based installer, then this provider for workers) | [`alibaba-openshift`](https://github.com/SammZhu/alibaba-openshift) — the ansible automation | See its [E2E-RUNBOOK](https://github.com/SammZhu/alibaba-openshift/blob/main/docs/E2E-RUNBOOK.md) |
| Provision **Alibaba Cloud block/file storage** (PV/PVC) via CSI | [`alibaba-cloud-csi-operator`](https://github.com/SammZhu/alibaba-cloud-csi-operator) | its own kind smoke |

Typical full-stack flow: `alibaba-openshift` installs the cluster and, as part of
its `site-post` step, deploys **this provider** (for workers) and the **CSI
operator** (for storage) onto it. If you're doing the whole thing, start at
`alibaba-openshift`; if you already have a cluster and just need workers, you're
in the right place.
