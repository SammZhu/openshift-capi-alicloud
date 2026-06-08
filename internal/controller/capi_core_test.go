package controller

import "testing"

func TestClassifyCAPICore(t *testing.T) {
	tests := []struct {
		name       string
		namespaces []string
		want       CAPICoreMode
	}{
		{"no core", nil, CAPICoreNone},
		{"empty slice", []string{}, CAPICoreNone},
		{"self-bundled capi-system", []string{"capi-system"}, CAPICoreBundled},
		{"self-bundled custom ns", []string{"cluster-api"}, CAPICoreBundled},
		{"OCP-hosted reused", []string{"openshift-cluster-api"}, CAPICoreReused},
		{"dual core conflict", []string{"capi-system", "openshift-cluster-api"}, CAPICoreConflict},
		{"three namespaces conflict", []string{"a", "b", "c"}, CAPICoreConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCAPICore(tc.namespaces); got != tc.want {
				t.Errorf("ClassifyCAPICore(%v) = %q, want %q", tc.namespaces, got, tc.want)
			}
		})
	}
}
