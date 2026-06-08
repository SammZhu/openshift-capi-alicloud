//go:build integration

// envtest integration harness (P3-CAPA.8 / #30). Boots a real kube-apiserver +
// etcd via controller-runtime's envtest and drives the reconcilers against it.
// The Alibaba cloud is served by pkg/client/fake, so these tests need no cloud.
//
// Gated behind the `integration` build tag: plain `go test ./...` (unit) does not
// compile this file and needs no envtest assets (stays air-gap friendly). Run with:
//
//	make test-integration
//
// Style: envtest + DIRECT Reconcile() calls (no background manager) for
// deterministic, race-free assertions — each test constructs its own reconciler
// with a per-test fake cloud and calls Reconcile synchronously.
package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
)

var (
	testEnv    *envtest.Environment
	k8sClient  client.Client
	testScheme *runtime.Scheme
)

func TestMain(m *testing.M) {
	logf.SetLogger(zap.New(zap.UseDevMode(true)))

	crdPaths := []string{filepath.Join("..", "..", "config", "crd", "bases")}
	if capi := os.Getenv("CAPI_CRD_DIR"); capi != "" {
		crdPaths = append(crdPaths, capi) // CAPI core Cluster/Machine CRDs
	}
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     crdPaths,
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest start failed: %v\n(set KUBEBUILDER_ASSETS — use `make test-integration`)\n", err)
		os.Exit(1)
	}

	testScheme = runtime.NewScheme()
	must(clientgoscheme.AddToScheme(testScheme))
	must(clusterv1.AddToScheme(testScheme))
	must(infrav1.AddToScheme(testScheme))

	k8sClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "k8s client: %v\n", err)
		_ = testEnv.Stop()
		os.Exit(1)
	}

	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// reconcileCtx returns a context for a single Reconcile call.
func reconcileCtx() context.Context { return context.Background() }
