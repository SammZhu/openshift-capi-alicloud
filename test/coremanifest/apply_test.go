//go:build integration

// Air-gap-independent risk reduction for the vendored CAPI core manifest
// (alibaba-openshift custom_manifests/cluster-api-core.yaml, produced by
// gen-cluster-api-core.py). Boots a real kube-apiserver via envtest and
// server-side-applies every document, so a broken CRD schema / resource / the
// service-ca webhook rewrites are caught BEFORE a live cluster run — without any
// cloud or air-gap dependency. Pods aren't scheduled (no kubelet); this checks the
// apply path only, which is exactly the bit that bit us repeatedly on the cluster.
//
// Run:
//   KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.31.0 -p path --bin-dir bin)" \
//   CORE_MANIFEST=~/openshift-alibaba/alibaba-openshift/custom_manifests/cluster-api-core.yaml \
//   go test -tags integration -v ./test/coremanifest/
package coremanifest

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestCoreManifestApplies(t *testing.T) {
	manifest := os.Getenv("CORE_MANIFEST")
	if manifest == "" {
		t.Skip("CORE_MANIFEST not set — point it at custom_manifests/cluster-api-core.yaml")
	}

	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest start failed (set KUBEBUILDER_ASSETS): %v", err)
	}
	defer func() { _ = env.Stop() }()

	cl, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	f, err := os.Open(manifest)
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer f.Close()

	ctx := context.Background()
	dec := yaml.NewYAMLOrJSONDecoder(f, 4096)
	var applied int
	var crdNames []string

	for {
		u := &unstructured.Unstructured{}
		if err := dec.Decode(&u.Object); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode: %v", err)
		}
		if len(u.Object) == 0 || u.GetKind() == "" {
			continue // comment-only / empty doc
		}
		// Server-side apply: validates schema + admission against a real apiserver.
		if err := cl.Patch(ctx, u, client.Apply,
			client.FieldOwner("coremanifest-test"), client.ForceOwnership); err != nil {
			t.Errorf("apply %s/%s rejected: %v", u.GetKind(), u.GetName(), err)
			continue
		}
		applied++
		if u.GetKind() == "CustomResourceDefinition" {
			crdNames = append(crdNames, u.GetName())
		}
	}

	if applied == 0 {
		t.Fatal("no resources applied — empty or mis-split manifest")
	}
	t.Logf("server-side applied %d resources (%d CRDs)", applied, len(crdNames))

	// Every CRD must reach Established=True — proves the CRD schema (incl. the
	// service-ca conversion-webhook stanza) is accepted and served.
	for _, name := range crdNames {
		var got apiextv1.CustomResourceDefinition
		established := false
		for i := 0; i < 30 && !established; i++ {
			if err := cl.Get(ctx, client.ObjectKey{Name: name}, &got); err != nil {
				t.Fatalf("get crd %s: %v", name, err)
			}
			for _, c := range got.Status.Conditions {
				if c.Type == apiextv1.Established && c.Status == apiextv1.ConditionTrue {
					established = true
				}
			}
			if !established {
				time.Sleep(200 * time.Millisecond)
			}
		}
		if !established {
			t.Errorf("CRD %s never reached Established=True", name)
		}
	}
	t.Logf("all %d CRDs Established — manifest applies cleanly", len(crdNames))
}
