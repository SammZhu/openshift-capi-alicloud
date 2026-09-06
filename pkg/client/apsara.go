package client

import (
	"net"
	"net/http"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/endpoints"
	"k8s.io/klog/v2"
)

// Apsara Stack (Alibaba's private-cloud distribution) speaks the same OpenAPI as
// the public cloud, but differs in three ways that the SDK cannot discover:
//
//  1. Endpoints.  The SDK derives ecs.<region>.aliyuncs.com, which simply does
//     not exist there; each service sits on an environment-specific host such as
//     ecs-internal.cloud.<env>.com.  Nothing in sdk.Config overrides this, so the
//     lookup fails and every call dies with "no such host" — which is exactly how
//     the CAPA controller failed to create instances.
//  2. Scheme.  Those gateways serve plain HTTP; a real TLS handshake is refused.
//  3. Headers.  Every request must carry the caller's organisation and resource
//     group.  They are NOT part of the ACS v1 signature (which covers query
//     parameters only), so they can be attached below the signing layer.
//
// All three are driven by environment variables, so a deployment configures them
// through the same secret that already supplies the credentials, and a public
// cloud deployment that sets none of them behaves exactly as before.
const (
	envApsaraScheme        = "ALIBABA_CLOUD_SCHEME"
	envApsaraOrganization  = "ALIBABA_CLOUD_ORGANIZATION_ID"
	envApsaraResourceGroup = "ALIBABA_CLOUD_RESOURCE_GROUP_ID"
	envApsaraCallerSource  = "ALIBABA_CLOUD_CALLER_SDK_SOURCE"
)

// apsaraEndpointEnv maps an SDK product id to the variable naming its endpoint.
// The product ids are the ones the SDK itself resolves with.
var apsaraEndpointEnv = map[string]string{
	"Ecs":             "ALIBABA_CLOUD_ENDPOINT_ECS",
	"Vpc":             "ALIBABA_CLOUD_ENDPOINT_VPC",
	"Slb":             "ALIBABA_CLOUD_ENDPOINT_SLB",
	"ResourceManager": "ALIBABA_CLOUD_ENDPOINT_RESOURCEMANAGER",
}

// headerInjector adds fixed headers to every request.
//
// It deliberately wraps a RoundTripper rather than being an *http.Transport: the
// SDK reaches for its transport with a `.(*http.Transport)` type assertion when
// applying per-request timeout, TLS and proxy settings, and that assertion fails
// here — which is what keeps it from replacing this wrapper mid-flight.  The
// flip side is that the SDK can no longer install a dial timeout, so the inner
// transport below carries its own.
type headerInjector struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerInjector) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone: RoundTrip must not modify the caller's request.
	r := req.Clone(req.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return h.base.RoundTrip(r)
}

func apsaraTransport(headers map[string]string) http.RoundTripper {
	return &headerInjector{
		base: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			Proxy:                 http.ProxyFromEnvironment,
		},
		headers: headers,
	}
}

// applyApsaraOverrides configures cfg for Apsara when the environment asks for
// it.  With no Apsara variables set it is a no-op, so the public cloud path is
// untouched.  Returns true when any override was applied.
func applyApsaraOverrides(cfg *sdk.Config, regionID string) bool {
	applied := false

	for product, env := range apsaraEndpointEnv {
		ep := firstNonEmpty(env)
		if ep == "" {
			continue
		}
		if err := endpoints.AddEndpointMapping(regionID, product, ep); err != nil {
			// Not fatal: the SDK falls back to its derived endpoint, which will
			// fail loudly on the first call rather than silently misbehave.
			klog.Errorf("apsara: could not map %s endpoint to %q: %v", product, ep, err)
			continue
		}
		klog.V(2).Infof("apsara: %s endpoint -> %s", product, ep)
		applied = true
	}

	if scheme := firstNonEmpty(envApsaraScheme); scheme != "" {
		cfg.Scheme = scheme
		klog.V(2).Infof("apsara: scheme -> %s", scheme)
		applied = true
	}

	headers := map[string]string{}
	if v := firstNonEmpty(envApsaraOrganization); v != "" {
		headers["x-acs-organizationid"] = v
	}
	if v := firstNonEmpty(envApsaraResourceGroup); v != "" {
		headers["x-acs-resourcegroupid"] = v
	}
	if len(headers) > 0 {
		headers["x-acs-regionid"] = regionID
		if v := firstNonEmpty(envApsaraCallerSource); v != "" {
			headers["x-acs-caller-sdk-source"] = v
		}
		cfg.Transport = apsaraTransport(headers)
		klog.V(2).Infof("apsara: injecting %d request headers", len(headers))
		applied = true
	}

	return applied
}
