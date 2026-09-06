package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
)

// A public-cloud deployment sets none of the Apsara variables and must be
// entirely unaffected — no scheme change, no transport, no endpoint mapping.
func TestApsaraOverridesAreNoOpWhenUnset(t *testing.T) {
	cfg := &sdk.Config{Scheme: "HTTPS"}
	if applyApsaraOverrides(cfg, "cn-hangzhou") {
		t.Fatal("reported overrides with no environment set")
	}
	if cfg.Scheme != "HTTPS" {
		t.Errorf("scheme changed to %q", cfg.Scheme)
	}
	if cfg.Transport != nil {
		t.Error("transport installed with no headers configured")
	}
}

func TestApsaraOverridesFromEnv(t *testing.T) {
	t.Setenv(envApsaraScheme, "HTTP")
	t.Setenv(envApsaraOrganization, "org-test")
	t.Setenv(envApsaraResourceGroup, "rs-test")
	t.Setenv(envApsaraCallerSource, "capi")
	t.Setenv(apsaraEndpointEnv["Ecs"], "ecs-internal.cloud.example.com")

	cfg := &sdk.Config{Scheme: "HTTPS"}
	if !applyApsaraOverrides(cfg, "cn-wulan-test-d01") {
		t.Fatal("overrides not reported as applied")
	}
	if cfg.Scheme != "HTTP" {
		t.Errorf("scheme = %q, want HTTP", cfg.Scheme)
	}
	if cfg.Transport == nil {
		t.Fatal("no transport installed despite headers being configured")
	}
	// The SDK swaps in its own transport when it can assert *http.Transport, so
	// the wrapper must NOT be one — that assertion failing is what protects it.
	if _, ok := cfg.Transport.(*http.Transport); ok {
		t.Error("transport is an *http.Transport; the SDK would replace it per request")
	}
}

func TestHeaderInjectorAddsHeadersWithoutMutatingRequest(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()

	rt := &headerInjector{
		base:    http.DefaultTransport,
		headers: map[string]string{"x-acs-organizationid": "org-1", "x-acs-regionid": "r-1"},
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got.Get("x-acs-organizationid") != "org-1" || got.Get("x-acs-regionid") != "r-1" {
		t.Errorf("headers not injected: %v", got)
	}
	// RoundTrip must leave the caller's request alone.
	if req.Header.Get("x-acs-organizationid") != "" {
		t.Error("RoundTrip mutated the caller's request")
	}
}
