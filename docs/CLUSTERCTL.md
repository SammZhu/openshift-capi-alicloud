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

The CRD/RBAC/controller/webhook manifests come from this repo's kustomize SSOT.
There are two overlays over the same base:
- `config/clusterctl` — **the clusterctl/vanilla-Kubernetes overlay** (webhook cert
  via cert-manager). This is what the release artifacts use.
- `config/default` — the OpenShift overlay (webhook cert via service-ca), built by
  Phase 08 and mirrored by the OLM bundle.

Render the clusterctl artifacts (use `make release`, which selects `config/clusterctl`):

```
make release   # -> out/{infrastructure-components,metadata,cluster-template}.yaml
# bake a real controller image (clusterctl has no ansible sed step):
#   CAPA_IMAGE=quay.io/samzhu/openshift-capi-alicloud:v0.1.23 make release
# under the hood: CAPA_KUSTOMIZE_DIR=config/clusterctl hack/gen-clusterctl-components.sh
```

It runs `kubectl kustomize config/clusterctl` and stamps every object with
`cluster.x-k8s.io/provider: infrastructure-alibabacloud` so clusterctl can
track/move/delete the provider. On a `v*` tag, CI publishes these three files as
GitHub Release assets automatically (see `.github/workflows/ci.yaml`).

## 2. Local-override layout

Point clusterctl at the artifacts via a provider entry + the overrides tree:

`~/.cluster-api/clusterctl.yaml`
```yaml
providers:
  - name: alibabacloud
    url: file:///home/you/.cluster-api/overrides/infrastructure-alibabacloud/v0.1.22/infrastructure-components.yaml
    type: InfrastructureProvider
```

`~/.cluster-api/overrides/infrastructure-alibabacloud/v0.1.22/`
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

## Kind smoke (verifies this install path)

`make test-clusterctl-smoke` (script: `hack/kind-smoke.sh`) exercises everything on
this page against a throwaway **kind** management cluster — hermetically, with **no
real Alibaba creds and no ECS provisioned**. It:

1. assembles the override layout (via `gen-clusterctl-components.sh` with a real
   pullable image baked in through `CAPA_IMAGE`),
2. `kind create cluster` + `clusterctl init --infrastructure alibabacloud`,
3. asserts the provider controller + webhooks come up (**install** path, G3-5),
4. `clusterctl generate cluster` renders the expected 6 objects and they are
   **admitted** by the webhooks,
5. the externally-managed `AlibabaCloudControlPlane` reconciles and CAPI core
   propagates `Cluster.status.initialization.controlPlaneInitialized=true`
   (**reconcile** smoke, G7-2 — needs no cloud creds),
6. the derived `AlibabaCloudMachine` reaches the Alibaba SDK and fails on the dummy
   creds without crashlooping (proves the path, not real provisioning).

Requires `kind` + `clusterctl` (>=v1.11 for the v1beta2 contract) + a container
runtime (docker, else podman with `KIND_EXPERIMENTAL_PROVIDER=podman` and the
machine bumped to ≥4GiB). `KEEP_CLUSTER=1` leaves the cluster up for debugging.

Note the webhook TLS: the clusterctl artifacts are rendered from the
`config/clusterctl` overlay, whose webhook serving cert is issued by **cert-manager**
(`clusterctl init` installs cert-manager as a hard dependency; its cainjector fills
each webhook's `caBundle` from the bundled `Issuer` + `Certificate`). This is the
portable, vanilla-Kubernetes path — no OpenShift service-ca and nothing to
self-sign. The OpenShift deploy (ansible `08-deploy-post-install`) and the OLM
bundle keep `config/default`, where the cert is minted by service-ca instead.

## Status / follow-up
- `metadata.yaml` + `cluster-template.yaml` are first-class in this repo.
- ✅ `config/` is now the single kustomize SSOT for the deployment manifests:
  `infrastructure-components.yaml` is generated from `config/default`, ansible
  (08-deploy-post-install) deploys `oc kustomize config/default`, and the OLM
  bundle CRDs mirror `config/crd/bases` (`make verify-manifests` enforces it).
  `make release` emits components + metadata + template into `out/`.
- ✅ Kind smoke implemented (`make test-clusterctl-smoke`, see above) — `clusterctl
  init` + external-CP reconcile both verified locally on kind off the config/ SSOT.
