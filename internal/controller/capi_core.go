package controller

import (
	"context"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CAPI core coexistence detection (P3-CAPA.29 / #79).
//
// This provider is a CAPI *infrastructure* provider only — it never embeds the
// Cluster API core (Cluster/Machine/MachineDeployment) controllers. Core is a
// separate deployment, supplied one of two ways:
//
//   - self-bundled: our ansible installs upstream cluster-api-components.yaml,
//     whose core controller-manager lands in namespace "capi-system";
//   - OCP-hosted:   the cluster-capi-operator manages an equivalent core in
//     namespace "openshift-cluster-api".
//
// Two cores running at once fight over the cluster.x-k8s.io CRDs, webhooks and
// leader election — a broken state, never a valid one (architecture risk #2).
//
// Detection signal (grounded, not assumed): upstream
// config/default/kustomization.yaml stamps the label
// cluster.x-k8s.io/provider=cluster-api onto every core resource — including the
// core controller-manager Deployment — via an includeSelectors label transformer.
// So the set of namespaces carrying a Deployment with that label is exactly the
// set of running CAPI cores. One namespace is fine; two or more is a conflict.

const (
	// capiCoreProviderLabel is the label upstream applies to every CAPI core
	// resource; we match the core controller-manager Deployment on it.
	capiCoreProviderLabel = "cluster.x-k8s.io/provider"
	// capiCoreProviderValue is the provider name carried by CAPI core (as opposed
	// to infrastructure/bootstrap/control-plane providers, which use other values).
	capiCoreProviderValue = "cluster-api"

	// ocpHostedCAPINamespace is where the OCP cluster-capi-operator runs its
	// managed CAPI core. A single core here means we are reusing the platform core.
	ocpHostedCAPINamespace = "openshift-cluster-api"
)

// CAPICoreMode classifies how this provider relates to the CAPI core running in
// the cluster.
type CAPICoreMode string

const (
	// CAPICoreBundled: exactly one core, in a non-OCP namespace — the self-bundled
	// core our ansible installs (default capi-system). Zero conflict.
	CAPICoreBundled CAPICoreMode = "Bundled"
	// CAPICoreReused: exactly one core, in the OCP-hosted namespace — we run
	// provider-only against the platform's cluster-capi-operator core.
	CAPICoreReused CAPICoreMode = "Reused"
	// CAPICoreConflict: two or more cores across distinct namespaces — mutual
	// exclusion; they will fight over cluster.x-k8s.io. Provider must not run.
	CAPICoreConflict CAPICoreMode = "Conflict"
	// CAPICoreNone: no core controller-manager Deployment found. Not necessarily an
	// error (CRDs may exist without the controller, or RBAC may hide Deployments);
	// the startup RESTMapping preflight already gates on the CRDs themselves.
	CAPICoreNone CAPICoreMode = "None"
)

// DetectCAPICoreNamespaces returns the sorted, de-duplicated set of namespaces
// running a CAPI core controller-manager, identified by the provider label.
func DetectCAPICoreNamespaces(ctx context.Context, c client.Reader) ([]string, error) {
	deploys := &appsv1.DeploymentList{}
	if err := c.List(ctx, deploys, client.MatchingLabels{capiCoreProviderLabel: capiCoreProviderValue}); err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for i := range deploys.Items {
		seen[deploys.Items[i].Namespace] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for ns := range seen {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out, nil
}

// ClassifyCAPICore maps a detected namespace set to a coexistence mode.
func ClassifyCAPICore(namespaces []string) CAPICoreMode {
	switch len(namespaces) {
	case 0:
		return CAPICoreNone
	case 1:
		if namespaces[0] == ocpHostedCAPINamespace {
			return CAPICoreReused
		}
		return CAPICoreBundled
	default:
		return CAPICoreConflict
	}
}
