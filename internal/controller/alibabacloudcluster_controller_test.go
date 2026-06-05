package controller

import (
	"context"
	"testing"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
)

// ── P3-CAPA.3 controlPlaneEndpoint spec→status mirror + BYO gate ─────────────

func TestReconcileControlPlaneEndpoint_MirrorsSpecToStatus(t *testing.T) {
	r := &AlibabaCloudClusterReconciler{}
	c := &infrav1.AlibabaCloudCluster{
		Spec: infrav1.AlibabaCloudClusterSpec{
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: "api-int.aliocp1.example.local",
				Port: 6443,
			},
		},
	}
	if err := r.reconcileControlPlaneEndpoint(context.Background(), c); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c.Status.ControlPlaneEndpoint.Host != "api-int.aliocp1.example.local" {
		t.Fatalf("status host not mirrored: %q", c.Status.ControlPlaneEndpoint.Host)
	}
	if c.Status.ControlPlaneEndpoint.Port != 6443 {
		t.Fatalf("status port not mirrored: %d", c.Status.ControlPlaneEndpoint.Port)
	}
}

func TestReconcileControlPlaneEndpoint_EmptySpecLeavesStatusEmpty(t *testing.T) {
	r := &AlibabaCloudClusterReconciler{}
	c := &infrav1.AlibabaCloudCluster{} // no spec endpoint
	if err := r.reconcileControlPlaneEndpoint(context.Background(), c); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c.Status.ControlPlaneEndpoint.Host != "" {
		t.Fatalf("expected empty status host, got %q", c.Status.ControlPlaneEndpoint.Host)
	}
}

// ── P3-CAPA.3 failure-domain projection (spec → status) ─────────────────────

func TestReconcileFailureDomains_ProjectsToStatus(t *testing.T) {
	r := &AlibabaCloudClusterReconciler{}
	c := &infrav1.AlibabaCloudCluster{
		Spec: infrav1.AlibabaCloudClusterSpec{
			FailureDomains: []infrav1.FailureDomain{
				{ZoneID: "cn-wulanchabu-a", VSwitchID: "vsw-a"},
				{ZoneID: "cn-wulanchabu-b", VSwitchID: "vsw-b"},
			},
		},
	}
	if err := r.reconcileFailureDomains(context.Background(), c); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(c.Status.FailureDomains) != 2 {
		t.Fatalf("want 2 failure domains, got %d", len(c.Status.FailureDomains))
	}
	if c.Status.FailureDomains[0].Attributes["vSwitchID"] != "vsw-a" {
		t.Fatalf("vSwitchID attribute not projected: %v", c.Status.FailureDomains[0].Attributes)
	}
}
