# Getting Started

A step-by-step guide to running Alibaba Cloud worker `Machine`s with this provider.

> **Read this first — scope.** This is an **alpha**, day-2 **worker-plane**
> provider with an **externally-managed control plane**. It does **not** create a
> control plane for you. You bring an existing control plane (any reachable
> Kubernetes API), pre-create some Alibaba Cloud infrastructure, and this provider
> manages the worker ECS instances against it. Unofficial / community-maintained.

## Which path is yours?

- **OpenShift on Alibaba Cloud** — use the companion
  [`alibaba-openshift`](https://github.com/SammZhu/alibaba-openshift) ansible flow
  (`site.yml` → `site-post.yml`). It wires up the mirror, control plane, this
  provider, CSI, and multi-AZ worker pools end to end (air-gap supported). That is
  the most-tested path; see its
  [docs](https://github.com/SammZhu/alibaba-openshift/blob/main/docs/README.md)
  (`CAPA-DAY2-OPS.md`, `CAPA-MULTI-AZ.md`). The rest of *this* guide is the
  generic clusterctl path.

- **Generic Cluster API (clusterctl)** — continue below.

---

## 1. Prerequisites

### 1.1 A management cluster + the provider installed

Any conformant Kubernetes cluster to run Cluster API on. Install CAPI core +
this provider with `clusterctl` — see **[CLUSTERCTL.md](CLUSTERCTL.md)**:

```sh
clusterctl init --infrastructure alibabacloud
```

`clusterctl init` also installs **cert-manager** (used for the provider's webhook
serving cert on vanilla Kubernetes).

### 1.2 A reachable control plane for the workers to join

The workers you create will join an **existing** Kubernetes/OpenShift control
plane. You need its API endpoint reachable from the worker subnet
(`CONTROL_PLANE_ENDPOINT_HOST[:PORT]`, port defaults to `6443`).

### 1.3 Alibaba Cloud infrastructure (pre-created)

Create these once and note the IDs — they become template variables in step 4.
(`aliyun` CLI examples; the console works too.)

| What | Variable | How to get the ID |
|---|---|---|
| Region | `ALIBABA_REGION` | e.g. `cn-wulanchabu` |
| Availability zone | `ALIBABA_AZ` | e.g. `cn-wulanchabu-a` |
| VPC + VSwitch (subnet) in that AZ | `ALIBABA_VSWITCH_ID` | `aliyun vpc DescribeVSwitches --RegionId <r> --ZoneId <az>` → `VSwitchId` |
| Security group (allow node↔control-plane + intra-cluster) | `ALIBABA_SECURITY_GROUP_ID` | `aliyun ecs DescribeSecurityGroups --RegionId <r>` → `SecurityGroupId` |
| RAM role attached to the worker ECS | `ALIBABA_RAM_ROLE_NAME` | the role **name** (not ARN) |
| Worker boot image (OS image the ECS boots) | `ALIBABA_BOOT_IMAGE_ID` | `aliyun ecs DescribeImages --RegionId <r> --ImageOwnerAlias self` → `ImageId` |

The boot image must be one your control plane can turn into a worker via the
bootstrap data (step 3) — e.g. an RHCOS/your-distro image whose ignition/cloud-init
fetches the join config. Verify the instance type is sellable in the AZ and
non-NVMe-only if your image is virtio:
`aliyun ecs DescribeAvailableResource --RegionId <r> --ZoneId <az> --DestinationResource InstanceType --InstanceType <type> --InstanceChargeType PostPaid`.

### 1.4 Controller credentials

The controller needs Alibaba Cloud credentials to manage ECS. The minimal RAM
policy and the credential modes (static AccessKey, RoleArn AssumeRole, ECS RAM
role) are in **[RAM-POLICY.md](RAM-POLICY.md)**. Simplest for a first run:

```sh
kubectl -n capa-system create secret generic alibaba-creds \
  --from-literal=ALIBABA_CLOUD_ACCESS_KEY_ID=$AK \
  --from-literal=ALIBABA_CLOUD_ACCESS_KEY_SECRET=$SK
```

---

## 2. Worker bootstrap data (you provide this)

> **Important honest caveat.** This provider does **not** bundle a CAPI *bootstrap*
> provider. It does not generate the node join configuration. You must supply the
> per-worker bootstrap data (the cloud-init / Ignition that makes a fresh ECS join
> *your* control plane) as a Secret, and reference it by name
> (`WORKER_BOOTSTRAP_SECRET`).

- **OpenShift**: the worker Ignition is the pointer config served by the cluster's
  machine-config server (`api-int…:22623/config/worker`). The `alibaba-openshift`
  flow handles this for you.
- **kubeadm / other**: generate the join config the way your control plane expects
  and put it in a Secret with a `value` key:

```sh
kubectl -n default create secret generic my-worker-bootstrap \
  --from-file=value=./worker-bootstrap.ign   # or your cloud-init userdata
```

The ECS receives this as user-data at boot.

---

## 3. Create a worker pool

Set the variables (from steps 1 & 2) and render the template, then apply:

```sh
export CLUSTER_NAME=demo NAMESPACE=default KUBERNETES_VERSION=v1.33.0
export ALIBABA_REGION=cn-wulanchabu ALIBABA_AZ=cn-wulanchabu-a
export ALIBABA_VSWITCH_ID=vsw-... ALIBABA_SECURITY_GROUP_ID=sg-...
export ALIBABA_RAM_ROLE_NAME=my-worker-role ALIBABA_BOOT_IMAGE_ID=m-...
export CONTROL_PLANE_ENDPOINT_HOST=10.0.0.10        # your control plane API
export WORKER_BOOTSTRAP_SECRET=my-worker-bootstrap
# optional: ALIBABA_INSTANCE_TYPE (default ecs.g7.xlarge), WORKER_MACHINE_COUNT (1),
#           CONTROL_PLANE_ENDPOINT_PORT (6443)

clusterctl generate cluster "$CLUSTER_NAME" --infrastructure alibabacloud \
  --kubernetes-version "$KUBERNETES_VERSION" | kubectl apply -f -
```

This renders six objects: `Cluster`, `AlibabaCloudCluster`,
`AlibabaCloudControlPlane` (external-managed), `AlibabaCloudMachineTemplate`,
`MachineDeployment`, `MachineHealthCheck`.

### Variable reference

| Variable | Required | Default | Notes |
|---|:---:|---|---|
| `CLUSTER_NAME` / `NAMESPACE` | ✓ | | CAPI Cluster name + namespace |
| `KUBERNETES_VERSION` | ✓ | | worker kubelet version, e.g. `v1.33.0` |
| `ALIBABA_REGION` / `ALIBABA_AZ` | ✓ | | region + AZ for the workers |
| `ALIBABA_VSWITCH_ID` | ✓ | | subnet in `ALIBABA_AZ` |
| `ALIBABA_SECURITY_GROUP_ID` | ✓ | | worker SG |
| `ALIBABA_RAM_ROLE_NAME` | ✓ | | RAM role **name** on the ECS |
| `ALIBABA_BOOT_IMAGE_ID` | ✓ | | worker OS image |
| `CONTROL_PLANE_ENDPOINT_HOST` | ✓ | | existing control plane API host |
| `WORKER_BOOTSTRAP_SECRET` | ✓ | | Secret with the node join data (step 2) |
| `CONTROL_PLANE_ENDPOINT_PORT` | | `6443` | |
| `ALIBABA_INSTANCE_TYPE` | | `ecs.g7.xlarge` | must be sellable in the AZ |
| `WORKER_MACHINE_COUNT` | | `1` | replicas |

For **multi-AZ HA**, run one `MachineDeployment` per AZ over a shared
`AlibabaCloudMachineTemplate`, each pinned to its `failureDomain` — see
[CLUSTERCTL.md](CLUSTERCTL.md) and the `alibaba-openshift`
`capa-worker-machinedeployment.yaml` example.

---

## 4. Verify

```sh
kubectl get machines -A                 # Machine should reach Running
kubectl get alibabacloudmachines -A     # status.ready, providerID set
kubectl get nodes -o wide               # the new worker joins + becomes Ready
kubectl get machinehealthcheck -A       # EXPECTED == CURRENT
```

A `Machine` reaches `Running` once the ECS is up **and** the Node has joined and
its `providerID` matches (`Machine.status.nodeRef` bound). If it stays
`Provisioned`, the ECS booted but the node hasn't joined — almost always a
bootstrap-data or networking issue (see below).

---

## 5. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Provider pod `CreateContainerConfigError` / webhook calls fail TLS | webhook cert not issued | On vanilla k8s cert-manager must be installed (`clusterctl init` does it). On OpenShift it's service-ca. |
| CAPA controller `CrashLoopBackOff`: *no matches for kind "Machine"* | CAPI **core** not installed | Install CAPI core (`clusterctl init` installs it; on OpenShift the ansible `08a` self-bundles it). |
| `Machine` stuck `Provisioned`, Node never appears, no CSR | worker can't fetch/apply bootstrap data, or can't reach the control plane | Check `WORKER_BOOTSTRAP_SECRET` is correct for your control plane; check the worker subnet routes to `CONTROL_PLANE_ENDPOINT_HOST` and DNS resolves; inspect ECS console output (`aliyun ecs GetInstanceConsoleOutput`). |
| RHCOS worker boots to emergency, ignition GET `403` from IMDS | IMDSv2 (`httpTokens=required`) blocks tokenless ignition | boot with IMDSv1 (the provider's worker template default; hardened to IMDSv2 after join — see G14). |
| Node joins but `Machine.status.nodeRef` stays empty | `Node.spec.providerID` ≠ `Machine.spec.providerID` format | both must be `alicloud://<region>.<instance-id>`; mismatched cloud-provider config. |
| `CreateStack`/RunInstances rejected: instance type not supported in zone / SoldOut | type not sellable in `ALIBABA_AZ` | pick a type available in that AZ (`DescribeAvailableResource`). |
| Clock-skew / TLS "certificate not yet valid" on the worker | no NTP egress in the worker subnet | ensure the worker subnet has NAT egress or an internal NTP source; chrony must sync before kubelet bootstrap. |

For deeper day-2 operations (scale, rolling update, drain, MHC remediation, IMDS
hardening, air-gap image strategy) see
[CAPA-DAY2-OPS.md](https://github.com/SammZhu/alibaba-openshift/blob/main/docs/CAPA-DAY2-OPS.md).
