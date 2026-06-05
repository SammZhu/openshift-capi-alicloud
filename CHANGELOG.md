# Changelog

All notable changes to this project are documented here. The format loosely
follows [Keep a Changelog](https://keepachangelog.com/), and the project aims to
follow semantic versioning once it reaches a stable API.

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
