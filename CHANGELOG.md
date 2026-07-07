# Changelog

All notable changes to this project are documented here. The format loosely
follows [Keep a Changelog](https://keepachangelog.com/), and the project aims to
follow semantic versioning once it reaches a stable API.

## [Unreleased]

- **security**: fix 4 reachable vulnerabilities found by `govulncheck` — bump
  `golang.org/x/net` to v0.56.0 (GO-2026-5026, idna) and the Go build toolchain
  to 1.26.4 (GO-2026-5037/5038/5039 in crypto/x509, mime, net/textproto).
- **ci**: add a `govulncheck` gate to the Build & Test job so future vulnerabilities
  (module + standard library) are caught on every push/PR.

## [v0.1.24] — clusterctl release + cert-manager (vanilla-Kubernetes installable)

- Publish the clusterctl discovery artifacts (`infrastructure-components.yaml` +
  `metadata.yaml` + `cluster-template.yaml`) as GitHub Release assets on every
  `v*` tag (CI `release` job).
- New `config/clusterctl` kustomize overlay: webhook serving cert via
  **cert-manager** (the CAPI standard) so the provider installs on any conformant
  Kubernetes cluster with `clusterctl init`. The OpenShift path (`config/default`,
  service-ca; ansible + OLM bundle) is unchanged.
- Verified end-to-end on kind (`make test-clusterctl-smoke`) and via `clusterctl`
  against the published release.

## [v0.1.23] — worker ECS identification

- Set the ECS `InstanceName` to the CAPI Machine name so worker instances are
  attributable in the Alibaba Cloud console (previously the opaque `iZ…Z` default).

## [v0.1.13 – v0.1.19] — multi-AZ worker plane + externally-managed control plane

- **v0.1.13**: full CAPI **v1beta2** infrastructure contract — contract labels on
  the infra CRDs + `status.initialization.provisioned`; fixes `Cluster.status.
  failureDomains` population and `infrastructureReady`.
- **v0.1.14 / v0.1.15**: `providerID` in CCM-aligned dotted form + persistence
  (immutable-once-set zoneID/vSwitchID webhook), so `Machine.spec.providerID`
  matches `Node.spec.providerID` and `nodeRef` binds.
- **v0.1.16**: `AlibabaCloudControlPlane` (`mode: external`) — externally-managed
  control plane that reports `controlPlaneInitialized`, unblocking
  `MachineDeployment` readiness for the worker-only topology.
- **v0.1.19**: worker IMDS hardening — boot with IMDSv1 (so RHCOS Ignition can
  fetch user-data) then flip the instance to IMDSv2 once it has joined.

## [v0.1.4 – v0.1.12] — worker pools, health checks, air-gap

- Multi-AZ worker pools via per-zone `MachineDeployment` + `failureDomains`;
  `MachineHealthCheck` (v1beta2 layout); scale-up/down with clean ECS reclaim.
- Self-bundled CAPI core controller for the worker-only topology (no managed CAPI
  on the target cluster).
- Air-gap image handling (digest-pinned controller image + IDMS/ITMS mirror
  redirects); single image-tag source of truth.
- OLM bundle + `clusterctl` artifacts (metadata, cluster-template) introduced.

## [v0.1.3] — CAPI contract compliance (PR1)

Container image: `quay.io/samzhu/openshift-capi-alicloud:v0.1.3`

### Machine controller (`AlibabaCloudMachine`)
- **Bootstrap data gate** (#24): requeue with reason `WaitingForBootstrapData`
  until the owning `Machine` has `spec.bootstrap.dataSecretName`. User-data is
  now read from the CAPI-standard `Machine.spec.bootstrap.dataSecretName` first,
  falling back to the legacy `AlibabaCloudMachine.spec.userDataSecret`.
- **providerID format fix** (#23): emit the CAPI-conformant
  `alicloud://<region>/<instanceID>` (slash separator). Earlier builds produced
  `alicloud://.<id>` from a dot separator and the often-empty `spec.regionID`,
  which the delete path could not parse — leaving the finalizer hung. Region is
  now resolved via `resolveRegion` (`spec.regionID`, else owning
  `Cluster.spec.region`). `regionFromMachine` parses both the new slash form and
  the legacy dot form for backward compatibility.

### Cluster controller (`AlibabaCloudCluster`)
- **`reconcileSLB` → `reconcileControlPlaneEndpoint`** (#25): mirror the BYO
  `spec.controlPlaneEndpoint → status.controlPlaneEndpoint`.
- **Ready gate** (#25): `status.ready` is set `true` only once
  `status.controlPlaneEndpoint.host` is populated; otherwise the cluster stays
  not-ready with reason `ControlPlaneEndpointMissing` and requeues.
- Added `ControlPlaneEndpoint` to `AlibabaCloudClusterStatus` and regenerated
  CRDs (`make generate`). **Gotcha:** the API server prunes status fields absent
  from the *deployed* CRD's OpenAPI schema, so the regenerated CRD must be
  re-applied or `status.controlPlaneEndpoint` is silently dropped.

### Both controllers
- **Paused annotation** (#28): honour `cluster.x-k8s.io/paused` via
  `annotations.IsPaused`, per the CAPI contract.

### Verification
Smoke-validated on a live SNO cluster (cn-wulanchabu): providerID rendered as
`alicloud://cn-wulanchabu/i-…`; delete parsed the region, removed the ECS
instance, and auto-cleared the finalizer (the prior P2 finalizer-hang is fixed);
bootstrap gate blocked `RunInstances` until data-secret present; cluster status
mirrored host/port and gated Ready correctly.

## [v0.1.2] — Explicit credential resolution

- **Credential resolution fix**: the Alibaba Cloud Go SDK's
  `NewClientWithOptions` returns `SDK.UnsupportedCredential` when passed a nil
  credential — it does not auto-discover the environment. Credentials are now
  resolved explicitly (`pkg/client/capi.go` → `resolveCredential`): AccessKey
  from `ALIBABA_CLOUD_ACCESS_KEY_{ID,SECRET}` (and the older `ALIBABACLOUD_*`
  spelling), then an ECS RAM role via `ALIBABA_CLOUD_ECS_METADATA`, then nil.
- Corrects the earlier, incorrect "no AK/SK required (RAM role instance
  principal)" claim in the docs.
- Avoid panicking in `init()` when the build-time version string is not semver.
