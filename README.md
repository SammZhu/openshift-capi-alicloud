# openshift-capi-alicloud

Cluster API Infrastructure Provider for Alibaba Cloud — designed for running
**OpenShift** on Alibaba Cloud ECS using the
[Cluster API (CAPI)](https://cluster-api.sigs.k8s.io/) framework.

## Overview

This provider implements the CAPI Infrastructure contract for Alibaba Cloud:

| Resource | API | Status |
|---|---|---|
| `AlibabaCloudCluster` | `infrastructure.cluster.x-k8s.io/v1beta1` | External Platform mode (pre-created VPC/SLB) |
| `AlibabaCloudMachine` | `infrastructure.cluster.x-k8s.io/v1beta1` | Fully implemented |
| `AlibabaCloudMachineTemplate` | `infrastructure.cluster.x-k8s.io/v1beta1` | Fully implemented |
| `AlibabaCloudClusterTemplate` | `infrastructure.cluster.x-k8s.io/v1beta1` | Spec only (no controller) |

### Design: External Platform mode

The primary use case is an OpenShift cluster installed via
**Agent-based or Assisted Installer** into a VPC and SLB pre-created by the
[ROS template](../alibaba-openshift/ros-templates/create-cluster.yaml) in the
`alibaba-openshift` repository.

In this mode:
- VPC and SLB already exist — the Cluster controller adopts them (no creation/deletion)
- CAPI manages **worker node lifecycle only**: scale out, scale in, health remediation
- Multi-AZ worker spreading is handled via `spec.failureDomains` in `AlibabaCloudCluster`

Authentication uses the **ECS RAM Role** instance principal — no AK/SK required.

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
- **Create**: `RunInstances` with instance type, image, vswitch, security groups, RAM role, user-data from Secret, tags
- **Delete**: `StopInstance` + `DeleteInstance` (force)
- **Status sync**: maps ECS instance status → CAPI Machine `Ready` condition
- **Failure domain resolution**: reads `Machine.spec.failureDomain`, looks up `AlibabaCloudCluster.status.failureDomains` to find zone + vswitch ID automatically (multi-AZ)

### Cluster Controller (`AlibabaCloudCluster`)

Implemented for the **External Platform** use case:
- **`reconcileVPC`**: copies `spec.vpcID → status.vpcID`; no VPC creation
- **`reconcileSLB`**: skips if `spec.controlPlaneEndpoint.host` is set; no SLB creation
- **`reconcileFailureDomains`**: publishes `spec.failureDomains → status.failureDomains` for CAPI to use during Machine assignment
- **`deleteSLB`**: skips if `status.slbInstanceID == ""`  (never set in External Platform mode)
- **`deleteVPC`**: only called when `spec.vpcID == ""` (i.e. never in External Platform mode)

> **Note**: Full VPC/SLB lifecycle management (create/delete) is not implemented.
> For the External Platform use case these stubs are complete and correct.

## Prerequisites

- OpenShift cluster installed on Alibaba Cloud (Agent-based or Assisted Installer)
- CAPI CRDs installed on the cluster
- ECS RAM Role with the following policies attached to worker nodes:
  - `AliyunECSFullAccess` (or scoped policy for RunInstances/StopInstance/DeleteInstance/DescribeInstances)
- RHCOS custom image imported into Alibaba Cloud as a custom image

## Quick Start

### 1. Install CAPI CRDs and controller

```sh
# Apply CRDs
kubectl apply -f config/crd/bases/

# Apply RBAC
kubectl apply -f config/rbac/

# Deploy controller (update image tag as needed)
kubectl apply -f config/manager/manager.yaml
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
        name: worker-user-data         # Secret with key "value" containing ignition JSON
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
