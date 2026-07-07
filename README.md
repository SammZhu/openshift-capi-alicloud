# openshift-capi-alicloud

A [Cluster API (CAPI)](https://cluster-api.sigs.k8s.io/) **infrastructure
provider for Alibaba Cloud**. It manages the lifecycle of Alibaba Cloud ECS
worker machines — create, scale, multi-AZ spread, health remediation, delete —
for a Cluster API management cluster, implementing the CAPI **v1beta2**
infrastructure contract.

It installs on any conformant Kubernetes management cluster via `clusterctl`, and
is also used in production for **OpenShift on Alibaba Cloud** (as the day-2 worker
plane on top of an externally-installed control plane).

> [!TIP]
> **New here? Run `make demo`.** It installs the provider on a throwaway local
> [kind](https://kind.sigs.k8s.io/) cluster and watches it reconcile in ~5
> minutes — **no Alibaba account, no ECS spend**. Then read
> **[docs/QUICKSTART.md](docs/QUICKSTART.md)** for real-cluster use and to figure
> out [which project you need](docs/QUICKSTART.md#3-which-project-do-i-need).

> [!IMPORTANT]
> **Unofficial, community-maintained project.** Not affiliated with, endorsed by,
> or supported by Alibaba Cloud, Red Hat, or OpenShift. "Alibaba Cloud" and
> "OpenShift" are trademarks of their respective owners and are used here only to
> describe the target platform. Licensed under Apache-2.0; provided as-is.

## Compatibility

| This provider | CAPI contract | CAPI core (vendored) | Kubernetes (tested) | clusterctl |
|---|---|---|---|---|
| `v0.1.x` | `v1beta2` | `v1.12.7` | 1.31 – 1.33 | ≥ `v1.11` |

Webhook serving certificates are issued by **cert-manager** on vanilla Kubernetes
(the `clusterctl` artifacts; `clusterctl init` installs cert-manager) or by
**OpenShift service-ca** on OpenShift (the ansible / OLM path). Same controller
image and contract on both — see [docs/CLUSTERCTL.md](docs/CLUSTERCTL.md).

## Overview

This provider implements the CAPI Infrastructure contract for Alibaba Cloud.
The CRDs are served at API version `v1beta1` and carry the `v1beta2` contract
label (`cluster.x-k8s.io/v1beta2`):

| Resource | API group/version | Status |
|---|---|---|
| `AlibabaCloudCluster` | `infrastructure.cluster.x-k8s.io/v1beta1` | External-platform mode (adopts pre-created VPC/SLB) |
| `AlibabaCloudMachine` | `infrastructure.cluster.x-k8s.io/v1beta1` | Fully implemented |
| `AlibabaCloudMachineTemplate` | `infrastructure.cluster.x-k8s.io/v1beta1` | Fully implemented |
| `AlibabaCloudControlPlane` | `controlplane.cluster.x-k8s.io/v1beta1` | Externally-managed control plane (`mode: external`) |
| `AlibabaCloudClusterTemplate` | `infrastructure.cluster.x-k8s.io/v1beta1` | Spec only (no controller) |

### Scope / maturity

Alpha (`v0.1.x`). Production scope today is the **day-2 worker plane + externally-
managed control plane**: the control plane is installed out-of-band (e.g. OpenShift
Assisted/Agent-based installer, or any existing cluster) and this provider manages
worker `Machine`s against it. Greenfield full-cluster provisioning (CAPI creating
the control plane) is not implemented. Single-maintainer, best-effort support.

### Design: External Platform mode

The primary use case is an OpenShift cluster installed via
**Agent-based or Assisted Installer** into a VPC and SLB pre-created by the
[ROS templates](https://github.com/SammZhu/alibaba-openshift/tree/main/ros-templates)
in the companion `alibaba-openshift` repository.

In this mode:
- VPC and SLB already exist — the Cluster controller adopts them (no creation/deletion)
- CAPI manages **worker node lifecycle only**: scale out, scale in, health remediation
- Multi-AZ worker spreading is handled via `spec.failureDomains` in `AlibabaCloudCluster`

### Authentication (since v0.1.2)

The Alibaba Cloud Go SDK's `NewClientWithOptions` does **not** auto-discover
credentials when passed a nil credential — it returns
`SDK.UnsupportedCredential`.  The controller resolves credentials explicitly,
in order (`pkg/client/capi.go` → `resolveCredential`):

1. **AccessKey from environment** — `ALIBABA_CLOUD_ACCESS_KEY_{ID,SECRET}`
   (also accepts the older `ALIBABACLOUD_*` spelling).  Inject via a Secret +
   `envFrom` on the controller Deployment — see the `alibaba-creds` pattern in
   `config/manager/deployment.yaml`.
2. **ECS RAM role** — when `ALIBABA_CLOUD_ECS_METADATA` names the role to assume.
3. nil — preserves loud-failure for an empty environment.

> Earlier docs claimed "no AK/SK required (RAM role instance principal)".
> That was never correct with the upstream SDK behaviour; fixed in v0.1.2.

## Architecture

```
ROS template
  └─ Creates: VPC, VSwitches, SLBs, Security Groups, RAM Roles

OpenShift install (Agent-based)
  └─ Installs control-plane nodes into the ROS-created infrastructure

CAPI (this provider)
  └─ AlibabaCloudCluster ─ adopts existing VPC + SLB endpoint
  └─ MachineDeployment   ─ manages worker ECS instances
       └─ scale / health-check / multi-AZ spreading
```

## Components

### Machine Controller (`AlibabaCloudMachine`)

Fully implemented. Handles:
- **Bootstrap gate** (since v0.1.3): requeues with `WaitingForBootstrapData`
  until the owning `Machine` has `spec.bootstrap.dataSecretName` set (CAPI
  contract) — so a node is never booted without its ignition/cloud-init.
- **Create**: `RunInstances` with instance type, image, vswitch, security
  groups, RAM role, tags, and user-data resolved from
  `Machine.spec.bootstrap.dataSecretName` (CAPI standard) with a fallback to
  the legacy `AlibabaCloudMachine.spec.userDataSecret`.
- **Region**: resolved via `resolveRegion` — `spec.regionID` if set, else the
  owning cluster's `spec.region` (machine RegionID is optional).
- **providerID**: CAPI-conformant `alicloud://<region>/<instanceID>` (slash).
  Earlier builds used a dot AND the often-empty `spec.regionID`, yielding
  `alicloud://.i-abc` which the delete path could not parse → finalizer hung.
  Fixed in v0.1.3; `regionFromMachine` still accepts the legacy dot form.
- **Delete**: resolves region from `spec.regionID` or the providerID, then
  `StopInstance` + `DeleteInstance` (force); finalizer is removed afterward.
- **Status sync**: maps ECS instance status → CAPI Machine `Ready` condition,
  publishes node addresses.
- **Failure domain resolution**: reads `Machine.spec.failureDomain`, looks up
  `AlibabaCloudCluster.status.failureDomains` to find zone + vswitch ID
  automatically (multi-AZ).
- **Paused**: honours the `cluster.x-k8s.io/paused` annotation
  (`annotations.IsPaused`).

### Cluster Controller (`AlibabaCloudCluster`)

Implemented for the **External Platform** use case:
- **`reconcileVPC`**: copies `spec.vpcID → status.vpcID`; no VPC creation
- **`reconcileControlPlaneEndpoint`** (renamed from `reconcileSLB` in v0.1.3):
  mirrors the BYO `spec.controlPlaneEndpoint → status.controlPlaneEndpoint`.
  This provider does **not** provision an SLB — the api-int endpoint (NLB /
  PrivateZone) is created out-of-band by ROS and supplied via the spec.
- **Ready gate** (since v0.1.3): `status.ready` is only set `true` once
  `status.controlPlaneEndpoint.host != ""` (CAPI infra-cluster contract).
  While empty, the cluster stays not-ready with reason
  `ControlPlaneEndpointMissing` and requeues, rather than reporting a cluster
  downstream controllers cannot reach.
- **`reconcileFailureDomains`**: publishes `spec.failureDomains → status.failureDomains` for CAPI to use during Machine assignment
- **`deleteSLB`**: skips if `status.slbInstanceID == ""`  (never set in External Platform mode)
- **`deleteVPC`**: only called when `spec.vpcID == ""` (i.e. never in External Platform mode)
- **Paused**: honours the `cluster.x-k8s.io/paused` annotation.

> **Note**: Full VPC/SLB lifecycle management (create/delete) is not implemented.
> For the External Platform use case these stubs are complete and correct.

### CAPI contract conditions

The controllers surface standard CAPI status reasons so `clusterctl describe`
and downstream automation behave predictably:

| Reason | Set by | Meaning |
|---|---|---|
| `WaitingForBootstrapData` | Machine | Owning `Machine` has no `spec.bootstrap.dataSecretName` yet; no ECS created. |
| `ControlPlaneEndpointMissing` | Cluster | `spec/status.controlPlaneEndpoint` is empty (BYO endpoint not supplied). |
| `ControlPlaneEndpointError` / `VPCReconcileError` / `FailureDomainError` | Cluster | The corresponding reconcile step failed. |
| `AlibabaClientError` | Cluster / Machine | Could not build the Alibaba Cloud SDK client (see Authentication). |

## Prerequisites

- OpenShift cluster installed on Alibaba Cloud (Agent-based or Assisted Installer)
- CAPI CRDs installed on the cluster
- Alibaba Cloud credentials for the controller (see [Authentication](#authentication-since-v012)):
  either an AccessKey pair injected via Secret + `envFrom`, or an ECS RAM role
  named through `ALIBABA_CLOUD_ECS_METADATA`. The principal needs
  `AliyunECSFullAccess` (or a scoped policy for
  RunInstances/StopInstance/DeleteInstance/DescribeInstances).
- RHCOS custom image imported into Alibaba Cloud as a custom image

## Quick Start

> **Start with [docs/QUICKSTART.md](docs/QUICKSTART.md)** — it walks you from a
> no-cloud `make demo` to real use, and points you to the right sibling project.
> For the full real-cluster walkthrough (Alibaba prerequisites, credentials,
> worker bootstrap data, verification, troubleshooting) see
> **[docs/GETTING-STARTED.md](docs/GETTING-STARTED.md)**. The steps below are the
> condensed version.

### 1. Install the provider

**Recommended — `clusterctl`** (works on any conformant Kubernetes management
cluster; installs cert-manager + CAPI core automatically):

```sh
# Tell clusterctl where to find this provider (released artifacts on GitHub):
cat >> ~/.cluster-api/clusterctl.yaml <<'EOF'
providers:
  - name: alibabacloud
    url: https://github.com/SammZhu/openshift-capi-alicloud/releases/latest/download/infrastructure-components.yaml
    type: InfrastructureProvider
EOF

clusterctl init --infrastructure alibabacloud
```

See [docs/CLUSTERCTL.md](docs/CLUSTERCTL.md) for the override layout, generating a
worker pool with `clusterctl generate cluster`, and the hermetic kind smoke test.

**Alternative — raw manifests** (e.g. OpenShift, where service-ca issues the
webhook cert; this is what the ansible flow applies):

```sh
kubectl apply -k config/default    # CRDs + RBAC + controller + webhooks (service-ca)
```

### 2. Create worker user-data Secret

Generate the worker ignition config and upload it:

```sh
# For Agent-based install, extract the worker ignition from the installer output
oc create secret generic worker-user-data \
  --from-file=value=worker.ign \
  -n openshift-cluster-api
```

### 3. Apply a MachineDeployment

See [`examples/capi-machinedeployment.yaml`](examples/capi-machinedeployment.yaml) for
complete single-AZ and multi-AZ examples.

Fill in all `FILL_IN` placeholders, then:

```sh
# Edit the example
cp examples/capi-machinedeployment.yaml my-workers.yaml
vi my-workers.yaml

# Apply
oc apply -f my-workers.yaml

# Watch machines come up
oc get alibabacloudmachines -A
oc get machines -A
```

### 4. Scale workers

```sh
oc scale machinedeployment CLUSTER_NAME-workers --replicas=5 -n openshift-cluster-api
```

## AlibabaCloudMachineTemplate spec reference

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: AlibabaCloudMachineTemplate
metadata:
  name: my-worker
  namespace: openshift-cluster-api
spec:
  template:
    spec:
      instanceType: ecs.c6.4xlarge     # required
      imageID: m-xxxx                  # required; RHCOS custom image ID
      regionID: cn-hangzhou            # required
      zoneID: cn-hangzhou-k            # optional; omit for multi-AZ failure domain
      vSwitchID: vsw-xxxx              # optional; omit for multi-AZ failure domain
      securityGroupIDs:
        - sg-xxxx                      # required; worker security group
      ramRoleName: my-cluster-node-ram-role  # required; ECS RAM role for instance principal
      systemDisk:
        category: cloud_essd           # cloud_essd | cloud_efficiency | cloud_ssd
        size: 120                      # GiB; minimum 120 for OpenShift
      tags:
        - key: kubernetes.io/cluster/CLUSTER_NAME
          value: owned
      userDataSecret:
        name: worker-user-data         # optional/legacy fallback — Secret with key
                                       # "value" containing ignition JSON. Normally
                                       # CAPI supplies user-data via the owning
                                       # Machine's spec.bootstrap.dataSecretName, so
                                       # this can be omitted for MachineDeployment-
                                       # managed workers.
      resourceGroupID: rg-xxxx         # optional
```

## Multi-AZ configuration

Set `spec.failureDomains` in `AlibabaCloudCluster` and omit `zoneID`/`vSwitchID`
from the `AlibabaCloudMachineTemplate`. CAPI will assign each Machine to a zone
in round-robin order, and the Machine controller resolves the vSwitch from the
cluster's failure domain list.

```yaml
# In AlibabaCloudCluster:
spec:
  failureDomains:
    - zoneID: cn-hangzhou-h
      vSwitchID: vsw-aaa
    - zoneID: cn-hangzhou-i
      vSwitchID: vsw-bbb
    - zoneID: cn-hangzhou-k
      vSwitchID: vsw-ccc
```

See `examples/capi-machinedeployment.yaml` — Scenario B for the full multi-AZ example.

## Documentation
| Doc | What |
|-----|------|
| [docs/GETTING-STARTED.md](docs/GETTING-STARTED.md) | **Start here** — end-to-end: prerequisites, install, credentials, worker bootstrap, create a pool, verify, troubleshooting. |
| [docs/CLUSTERCTL.md](docs/CLUSTERCTL.md) | Install via `clusterctl` (metadata / components / cluster-template) + the kind smoke. |
| [docs/OPERATOR-BUNDLE.md](docs/OPERATOR-BUNDLE.md) | OLM bundle (CSV / owned CRDs / webhookdefinitions) — community-operator prep. |
| [docs/RAM-POLICY.md](docs/RAM-POLICY.md) | Controller credential modes (ECS RAM role / RoleArn AssumeRole / AccessKey) + minimal RAM policy. |
| [docs/INTEGRATION-TESTS.md](docs/INTEGRATION-TESTS.md) | envtest integration harness (`make test-integration`). |

> `config/` is the kustomize **SSOT** for the deployment manifests (CRDs + RBAC +
> controller + webhooks); ansible, the OLM bundle, and clusterctl all build from it.
> The day-2 operational guides (scale / autoscaler / multi-AZ / worker-join) live in
> the sibling `alibaba-openshift/docs/` — see its [docs index](https://github.com/SammZhu/alibaba-openshift/blob/main/docs/README.md).

## Development

### Build

```sh
# Build local binary
make build

# Run tests
make test

# Build container image (requires podman or docker)
make image IMAGE=quay.io/myorg/openshift-capi-alicloud:dev
make push  IMAGE=quay.io/myorg/openshift-capi-alicloud:dev
```

### Regenerate CRDs and DeepCopy

```sh
make generate
```

### Project layout

```
api/v1beta1/          API types (AlibabaCloudCluster, AlibabaCloudMachine, ...)
cmd/                  Manager entrypoint
config/
  crd/bases/          Generated CRD YAML
  rbac/               RBAC manifests
  manager/            Controller Deployment manifest
examples/             Ready-to-use MachineDeployment examples
internal/controller/  Cluster + Machine reconcilers
pkg/client/           Alibaba Cloud SDK wrapper (ECS, VPC, SLB, RAM)
pkg/utils/            Ignition user-data helpers
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache License 2.0 — see [LICENSE](LICENSE).
