/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhook

import (
	"context"
	"testing"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	infrav1 "github.com/SammZhu/openshift-capi-alicloud/api/v1beta1"
)

func strPtr(s string) *string { return &s }

func validMachine() *infrav1.AlibabaCloudMachine {
	return &infrav1.AlibabaCloudMachine{
		Spec: infrav1.AlibabaCloudMachineSpec{
			InstanceType: "ecs.g7.large",
			ImageID:      "m-abc",
			SystemDisk:   &infrav1.SystemDisk{Category: "cloud_essd", Size: 40},
		},
	}
}

// ── Defaulting ──────────────────────────────────────────────────────────────

func TestMachineDefault_NilSystemDisk(t *testing.T) {
	m := &infrav1.AlibabaCloudMachine{Spec: infrav1.AlibabaCloudMachineSpec{InstanceType: "ecs.g7.large", ImageID: "m-abc"}}
	if err := (&AlibabaCloudMachineWebhook{}).Default(context.Background(), m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Spec.SystemDisk == nil {
		t.Fatal("systemDisk should have been defaulted")
	}
	if m.Spec.SystemDisk.Category != defaultSystemDiskCategory || m.Spec.SystemDisk.Size != defaultSystemDiskSizeGiB {
		t.Errorf("got %+v, want {%s %d}", *m.Spec.SystemDisk, defaultSystemDiskCategory, defaultSystemDiskSizeGiB)
	}
}

func TestMachineDefault_PartialSystemDisk(t *testing.T) {
	m := &infrav1.AlibabaCloudMachine{Spec: infrav1.AlibabaCloudMachineSpec{
		InstanceType: "ecs.g7.large", ImageID: "m-abc",
		SystemDisk: &infrav1.SystemDisk{Size: 100}, // category empty, keep size
	}}
	if err := (&AlibabaCloudMachineWebhook{}).Default(context.Background(), m); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Spec.SystemDisk.Category != defaultSystemDiskCategory {
		t.Errorf("category not defaulted: %q", m.Spec.SystemDisk.Category)
	}
	if m.Spec.SystemDisk.Size != 100 {
		t.Errorf("explicit size must be preserved, got %d", m.Spec.SystemDisk.Size)
	}
}

func TestMachineDefault_WrongType(t *testing.T) {
	if err := (&AlibabaCloudMachineWebhook{}).Default(context.Background(), &infrav1.AlibabaCloudCluster{}); err == nil {
		t.Fatal("expected an error for a non-machine object")
	}
}

// ── Machine validation (create) ─────────────────────────────────────────────

func TestMachineValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*infrav1.AlibabaCloudMachine)
		wantErr bool
	}{
		{"valid", func(*infrav1.AlibabaCloudMachine) {}, false},
		{"empty instanceType", func(m *infrav1.AlibabaCloudMachine) { m.Spec.InstanceType = "" }, true},
		{"bad instanceType prefix", func(m *infrav1.AlibabaCloudMachine) { m.Spec.InstanceType = "g7.large" }, true},
		{"empty imageID", func(m *infrav1.AlibabaCloudMachine) { m.Spec.ImageID = "" }, true},
		{"system disk too small", func(m *infrav1.AlibabaCloudMachine) { m.Spec.SystemDisk.Size = 10 }, true},
		{"data disk too small", func(m *infrav1.AlibabaCloudMachine) {
			m.Spec.DataDisks = []infrav1.DataDisk{{Category: "cloud_essd", Size: 10}}
		}, true},
		{"duplicate tag", func(m *infrav1.AlibabaCloudMachine) {
			m.Spec.Tags = []infrav1.Tag{{Key: "k", Value: "1"}, {Key: "k", Value: "2"}}
		}, true},
		{"empty tag key", func(m *infrav1.AlibabaCloudMachine) {
			m.Spec.Tags = []infrav1.Tag{{Key: "", Value: "1"}}
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := validMachine()
			tc.mutate(m)
			_, err := (&AlibabaCloudMachineWebhook{}).ValidateCreate(context.Background(), m)
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v got err=%v", tc.wantErr, err)
			}
		})
	}
}

// ── Machine validation (update / immutability) ──────────────────────────────

func TestMachineValidateUpdate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(oldM, newM *infrav1.AlibabaCloudMachine)
		wantErr bool
	}{
		{"no change", func(_, _ *infrav1.AlibabaCloudMachine) {}, false},
		{"change instanceType", func(_, n *infrav1.AlibabaCloudMachine) { n.Spec.InstanceType = "ecs.g8.xlarge" }, true},
		{"change imageID", func(_, n *infrav1.AlibabaCloudMachine) { n.Spec.ImageID = "m-other" }, true},
		{"change zoneID", func(o, n *infrav1.AlibabaCloudMachine) { o.Spec.ZoneID = "z-a"; n.Spec.ZoneID = "z-b" }, true},
		{"change systemDisk", func(_, n *infrav1.AlibabaCloudMachine) { n.Spec.SystemDisk.Size = 80 }, true},
		{"providerID set first time", func(o, n *infrav1.AlibabaCloudMachine) {
			o.Spec.ProviderID = nil
			n.Spec.ProviderID = strPtr("alicloud://cn-hangzhou/i-1")
		}, false},
		{"providerID changed", func(o, n *infrav1.AlibabaCloudMachine) {
			o.Spec.ProviderID = strPtr("alicloud://cn-hangzhou/i-1")
			n.Spec.ProviderID = strPtr("alicloud://cn-hangzhou/i-2")
		}, true},
		{"providerID cleared", func(o, n *infrav1.AlibabaCloudMachine) {
			o.Spec.ProviderID = strPtr("alicloud://cn-hangzhou/i-1")
			n.Spec.ProviderID = nil
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldM, newM := validMachine(), validMachine()
			tc.mutate(oldM, newM)
			_, err := (&AlibabaCloudMachineWebhook{}).ValidateUpdate(context.Background(), oldM, newM)
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v got err=%v", tc.wantErr, err)
			}
		})
	}
}

func TestMachineValidateDelete_AlwaysAllowed(t *testing.T) {
	if _, err := (&AlibabaCloudMachineWebhook{}).ValidateDelete(context.Background(), validMachine()); err != nil {
		t.Fatalf("delete must be allowed, got %v", err)
	}
}

// ── Cluster validation ──────────────────────────────────────────────────────

func validCluster() *infrav1.AlibabaCloudCluster {
	return &infrav1.AlibabaCloudCluster{Spec: infrav1.AlibabaCloudClusterSpec{Region: "cn-hangzhou"}}
}

func TestClusterValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*infrav1.AlibabaCloudCluster)
		wantErr bool
	}{
		{"valid", func(*infrav1.AlibabaCloudCluster) {}, false},
		{"empty region", func(c *infrav1.AlibabaCloudCluster) { c.Spec.Region = "" }, true},
		{"endpoint host no port", func(c *infrav1.AlibabaCloudCluster) {
			c.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{Host: "api.example.com"}
		}, true},
		{"endpoint port no host", func(c *infrav1.AlibabaCloudCluster) {
			c.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{Port: 6443}
		}, true},
		{"endpoint valid", func(c *infrav1.AlibabaCloudCluster) {
			c.Spec.ControlPlaneEndpoint = clusterv1.APIEndpoint{Host: "api.example.com", Port: 6443}
		}, false},
		{"duplicate failure domain zone", func(c *infrav1.AlibabaCloudCluster) {
			c.Spec.FailureDomains = []infrav1.FailureDomain{
				{ZoneID: "z-a", VSwitchID: "vsw-1"},
				{ZoneID: "z-a", VSwitchID: "vsw-2"},
			}
		}, true},
		{"failure domain missing vSwitch", func(c *infrav1.AlibabaCloudCluster) {
			c.Spec.FailureDomains = []infrav1.FailureDomain{{ZoneID: "z-a"}}
		}, true},
		{"failure domains valid", func(c *infrav1.AlibabaCloudCluster) {
			c.Spec.FailureDomains = []infrav1.FailureDomain{
				{ZoneID: "z-a", VSwitchID: "vsw-1"},
				{ZoneID: "z-b", VSwitchID: "vsw-2"},
			}
		}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validCluster()
			tc.mutate(c)
			_, err := (&AlibabaCloudClusterWebhook{}).ValidateCreate(context.Background(), c)
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v got err=%v", tc.wantErr, err)
			}
		})
	}
}

func TestClusterValidateUpdate_RegionImmutable(t *testing.T) {
	oldC := validCluster()
	newC := validCluster()
	newC.Spec.Region = "cn-shanghai"
	if _, err := (&AlibabaCloudClusterWebhook{}).ValidateUpdate(context.Background(), oldC, newC); err == nil {
		t.Fatal("expected region change to be rejected")
	}
	// Same region but other mutable change is fine.
	newC2 := validCluster()
	newC2.Spec.VpcID = "vpc-new"
	if _, err := (&AlibabaCloudClusterWebhook{}).ValidateUpdate(context.Background(), oldC, newC2); err != nil {
		t.Fatalf("non-region update should be allowed, got %v", err)
	}
}
