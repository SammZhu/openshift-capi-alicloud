# Using this provider with clusterctl

This provider ships the three artifacts `clusterctl` needs:

| File | Role |
|------|------|
| `metadata.yaml` | release-series → CAPI API contract (`v1beta2`) |
| `infrastructure-components.yaml` | the deployable provider (CRDs + controller + RBAC + webhooks), generated |
| `templates/cluster-template.yaml` | parameterized day-2 worker pool for `clusterctl generate cluster` |

> Positioning: we are a **day-2 worker / externally-managed control plane** provider.
> `clusterctl` here installs the provider and generates worker-pool manifests; the
> OCP control plane itself is installed out-of-band (Assisted Installer + ROS). The
> primary supported install path remains the ansible flow (`08-deploy-post-install`);
> clusterctl is the CAPI-native alternative entrypoint.

## 1. Generate the components

The canonical CRD/controller/RBAC/webhook manifests live in the sibling
`alibaba-openshift/custom_manifests/` (the same files Phase 08 applies). Assemble
them into a clusterctl-labelled `infrastructure-components.yaml`:

```
hack/gen-clusterctl-components.sh out/infrastructure-components.yaml
# override the source dir if the repos aren't siblings:
#   CAPA_MANIFESTS_DIR=/path/to/custom_manifests hack/gen-clusterctl-components.sh ...
```

It stamps every object with `cluster.x-k8s.io/provider: infrastructure-alibabacloud`
so clusterctl can track/move/delete the provider.

## 2. Local-override layout

Point clusterctl at the artifacts via a provider entry + the overrides tree:

`~/.cluster-api/clusterctl.yaml`
```yaml
providers:
  - name: alibabacloud
    url: file:///home/you/.cluster-api/overrides/infrastructure-alibabacloud/v0.1.19/infrastructure-components.yaml
    type: InfrastructureProvider
```

`~/.cluster-api/overrides/infrastructure-alibabacloud/v0.1.19/`
```
infrastructure-components.yaml   # from step 1
metadata.yaml                    # repo root
cluster-template.yaml            # templates/cluster-template.yaml
```

## 3. Init + generate a worker pool

```
# Cluster API core must already be present (we self-bundle it via 08a, or use
# clusterctl init's core provider on a management cluster).
clusterctl init --infrastructure alibabacloud

# Provider Secret (AK/SK) as usual:
kubectl -n capa-system create secret generic alibaba-creds \
  --from-literal=ALIBABA_CLOUD_ACCESS_KEY_ID=$AK \
  --from-literal=ALIBABA_CLOUD_ACCESS_KEY_SECRET=$SK

# Generate a worker pool (variables below) and apply:
clusterctl generate cluster caworkers --infrastructure alibabacloud \
  --kubernetes-version v1.33.0 | kubectl apply -f -
```

### Template variables
Required: `CLUSTER_NAME`, `NAMESPACE`, `KUBERNETES_VERSION`, `ALIBABA_REGION`,
`ALIBABA_AZ`, `ALIBABA_VSWITCH_ID`, `ALIBABA_BOOT_IMAGE_ID`,
`ALIBABA_SECURITY_GROUP_ID`, `ALIBABA_RAM_ROLE_NAME`, `CONTROL_PLANE_ENDPOINT_HOST`,
`WORKER_BOOTSTRAP_SECRET`.
Defaulted: `CONTROL_PLANE_ENDPOINT_PORT=6443`, `ALIBABA_INSTANCE_TYPE=ecs.g7.xlarge`,
`WORKER_MACHINE_COUNT=1`.

`cluster-template.yaml` is the SINGLE-AZ form. For multi-AZ HA, add the AZ to
`AlibabaCloudCluster.spec.failureDomains` and add one MachineDeployment per AZ
(reusing the shared AlibabaCloudMachineTemplate, each with its own `failureDomain`)
— same shape as `alibaba-openshift/custom_manifests/capa-worker-machinedeployment.yaml`.
See [CAPA-DAY2-OPS](https://github.com/SammZhu/alibaba-openshift/blob/main/docs/CAPA-DAY2-OPS.md).

## Status / follow-up
- `metadata.yaml` + `cluster-template.yaml` are first-class in this repo.
- `infrastructure-components.yaml` is **generated** from the ansible manifests (no
  third hand-maintained copy). The structural follow-up is to make the provider
  repo's `config/` the single kustomize source of the deployment manifests and wire
  a `make release` that emits components + copies metadata/templates — at which
  point ansible/OLM/clusterctl all consume one source. (`config/` today is a
  legacy CCCMO-era layout, not the current CAPI deployment.)
- A kind smoke (`clusterctl init --infrastructure alibabacloud`) is the suggested
  verification once components are published.
