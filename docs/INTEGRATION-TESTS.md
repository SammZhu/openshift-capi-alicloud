# Integration tests (envtest) — P3-CAPA.8 / #30

These run the reconcilers against a **real kube-apiserver + etcd** (controller-runtime
`envtest`), with the Alibaba cloud served by `pkg/client/fake`. They catch what
fake-client unit tests cannot: CRD schema/validation (required fields,
`minProperties`, defaulting), finalizer + status patch round-trips, owner-ref
resolution, and paused/owner gating — i.e. the real API contract.

## Running

```sh
make test-integration      # downloads setup-envtest + k8s test binaries, then runs
```

- Gated behind the `integration` build tag, so plain `make test` (unit) needs **no
  envtest assets** and stays air-gap friendly. Default `go test ./...` skips these.
- CAPI core CRDs (Cluster/Machine) are loaded from the `cluster-api` module in the
  cache (`CAPI_CRD_DIR`); our CRDs from `config/crd/bases`.
- Air-gapped runners: pre-stage the envtest binaries and set `KUBEBUILDER_ASSETS`;
  `setup-envtest` also supports an offline mirror.

## Design

`envtest` + **direct `Reconcile()` calls** (no background manager): each test creates
objects via the API, calls `Reconcile` synchronously, and asserts the persisted
result. Deterministic and race-free — no `Eventually` polling.

## Coverage matrix

| Area | Case | Status |
|---|---|---|
| Cluster | BYO endpoint → finalizer + Ready=True | ✅ |
| Cluster | paused annotation → reconcile skipped, no finalizer | ✅ |
| Cluster | (missing endpoint → ControlPlaneEndpointMissing) | covered by unit tests — unreachable via API (CRD enforces `minProperties:1` on `controlPlaneEndpoint`) |
| Machine | infra cluster not Ready → finalizer + Ready=False/ClusterInfrastructureNotReady + requeue | ✅ |
| Machine | cluster Ready, no bootstrap (ConfigRef set, DataSecretName nil) → Ready=False/WaitingForBootstrapData + requeue | ✅ |
| Machine | paused annotation → reconcile skipped, no finalizer | ✅ |
| Machine | bootstrap ready → CreateECSInstance → providerID set | covered by `createInstance` unit tests; full-Reconcile happy path ⏳ planned |
| Machine | delete → waits for Terminated before finalizer removal | ⏳ planned |
| CSR | CAPA-backed node CSR auto-approved | ⏳ planned |

## Notes / findings

- The v1beta2 CAPI `Cluster.spec` uses JSON `omitzero`; an empty spec serialises to
  `null` and the CRD rejects it. Tests set a minimal non-zero spec (`paused: false`).
- Our `AlibabaCloudCluster` CRD enforces `controlPlaneEndpoint` at creation
  (`minProperties:1`), so the reconciler's "endpoint missing → requeue" branch is
  defensive and cannot be reached through the API.
