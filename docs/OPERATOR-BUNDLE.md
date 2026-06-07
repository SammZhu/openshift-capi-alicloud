# OLM bundle (community-operator prep — P6-DIST.0)

A ready-to-iterate **OLM bundle** for the channel-B distribution path (OperatorHub
community operator). This is *preparation only*: the bundle validates locally and
its metadata is complete, but the PR to the community-operators repos must wait
until the operator is proven on a live cluster (the hosted pipeline deploys it).

> **Do not submit the PR yet.** This bundle is content/packaging; on-cluster
> behavior is validated separately (B2/#69 + hardening). Submit after that passes.

## Layout

```
bundle/
  manifests/
    cluster-api-provider-alibabacloud.clusterserviceversion.yaml   # CSV
    infrastructure.cluster.x-k8s.io_alibabacloud{clusters,machines,*templates}.yaml
  metadata/annotations.yaml          # package, channels, com.redhat.openshift.versions
  tests/scorecard/config.yaml        # scorecard suite
bundle.Dockerfile                    # builds the bundle image
```

- Package: `cluster-api-provider-alibabacloud`, channel `alpha`, version `0.1.12`.
- OCP range: `com.redhat.openshift.versions: v4.16-v4.20`.
- Install modes: `AllNamespaces` (cluster-scoped provider) + `OwnNamespace`.
- Owned CRDs: AlibabaCloud{Cluster,Machine,ClusterTemplate,MachineTemplate}; `alm-examples` for each.
- The CSV `install` embeds the controller Deployment + clusterPermissions
  (transcribed from `custom_manifests/02-capa-controller.yaml`).
- `webhookdefinitions`: the 1 mutating + 2 validating webhooks (from
  `02-capa-webhooks.yaml`); under OLM the **service-ca cert volume is removed** —
  OLM provisions the webhook Service + serving cert itself.

## Validate locally

```bash
operator-sdk bundle validate ./bundle \
  --select-optional suite=operatorframework \
  --select-optional name=community \
  --select-optional name=good-practices
# -> "All validation tests have completed successfully"
```

## On-cluster caveats to resolve before submitting (deferred)

These are **validation-time / P6-DIST.2-3** items, not fixable offline:

1. **Webhook cert path** — controller-runtime serves on `:9443` reading certs from
   `/tmp/k8s-webhook-server/serving-certs`. OLM injects its own cert volume; verify
   the mount path matches (or set `--webhook-cert-dir`). This is the cert-manager/
   OLM webhook decoupling tracked in P6-DIST.2.
2. **CAPI core dependency** — the operator requires Cluster API core CRDs
   (Cluster/Machine/MachineDeployment). OLM auto-resolution does NOT cover it (CAPI
   core is not an OperatorHub package), so document as a hard prerequisite rather
   than declaring `olm.gvk required` (a pre-existing CRD may not satisfy OLM's
   resolver and can wedge the subscription). P6-DIST.3.
3. **Alibaba cloud-controller-manager (CCM)** — a **runtime prerequisite**, NOT a
   code dependency. CAPA creates the ECS and the node joins, but the CCM is what
   removes the `node.cloudprovider.kubernetes.io/uninitialized` taint and sets the
   Node providerID/addresses/zone labels + Service load balancers. Without it,
   provisioned workers stay unschedulable and their Machines never reach Running.
   The CCM is a DaemonSet/Deployment (not an OLM operator) so OLM cannot install or
   gate on it — document it, and surface a runtime degraded condition when it is
   missing (see P3-CAPA.27). In the OpenShift-native path (P6-DIST.1/path 2) the CCM
   is managed by `cluster-cloud-controller-manager-operator` at the platform level.
4. **Credentials Secret** — the Deployment mounts `alibaba-creds` (`optional:false`);
   the operator's namespace must have it before the pod starts. Document in the CSV
   description / install notes.

## Image-hygiene audit (P6-DIST.0 item 5)

| Check | Status | Note |
|---|---|---|
| `runAsNonRoot` | ✅ | `Dockerfile`: distroless/static:nonroot, `USER 65532` |
| no `:latest` | ✅ | image pinned to `:v0.1.12` |
| resources requests/limits | ✅ | in the Deployment |
| liveness/readiness probes | ✅ | `/healthz` `/readyz` on `:9440` |
| leader election | ✅ | `--leader-elect=true` |
| UBI base image | ⚠️ | `Dockerfile` is distroless; `Dockerfile.rhel` is UBI/RHEL — use the RHEL build for certified (P6-DIST.4) |
| image by digest | ⚠️ | CSV uses a tag; pin `containerImage` + `relatedImages` to a digest (P6-DIST.4) |
| multi-arch (amd64+arm64) | ⚠️ | confirm CI buildx emits arm64 (P6-DIST.4) |

✅ = ready now · ⚠️ = deferred to P6-DIST.4 (certification readiness), not a blocker
for the community channel.
