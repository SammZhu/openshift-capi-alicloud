package client

import (
	"errors"
	"math/rand"
	"strings"
	"time"

	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"k8s.io/klog/v2"

	"github.com/SammZhu/openshift-capi-alicloud/pkg/metrics"
)

// Throttle-retry tunables. The Alibaba OpenAPI returns rate-limit errors as 4xx
// ServerErrors with a Throttling* code, which the SDK's built-in AutoRetry (5xx
// /transport only, no backoff) does not cover. At scale — e.g. a MachineSet
// scaling many replicas at once — bursts of RunInstances/DescribeInstances trip
// these, so we retry them here with capped exponential backoff + jitter instead
// of bubbling every transient throttle up as a reconcile error.
//
// Declared as vars (not consts) so tests can shrink the delays.
var (
	throttleMaxRetries = 5
	throttleBaseDelay  = 200 * time.Millisecond
	throttleMaxDelay   = 5 * time.Second
	// throttleSleep is indirected for tests.
	throttleSleep = time.Sleep
)

// isThrottlingError reports whether err is a retryable Alibaba Cloud throttling
// / temporary rate-limit error.
func isThrottlingError(err error) bool {
	if err == nil {
		return false
	}
	var srv *sdkerrors.ServerError
	if errors.As(err, &srv) {
		code := srv.ErrorCode()
		if strings.Contains(code, "Throttling") {
			return true
		}
		switch code {
		case "ServiceUnavailable", "RequestThrottled", "RequestLimitExceeded":
			return true
		}
		if hs := srv.HttpStatus(); hs == 429 || hs == 503 {
			return true
		}
	}
	return false
}

// retryThrottled runs fn, retrying only Alibaba Cloud throttling errors with
// capped exponential backoff + jitter. Non-throttling errors (and success)
// return immediately, so terminal/business errors are never masked.
func retryThrottled(op string, fn func() error) error {
	start := time.Now()
	delay := throttleBaseDelay
	var err error
	for attempt := 0; ; attempt++ {
		if err = fn(); err == nil || !isThrottlingError(err) {
			metrics.ObserveCloudAPI(op, time.Since(start).Seconds(), err)
			return err
		}
		// Throttled. If the budget is exhausted, give up.
		if attempt >= throttleMaxRetries {
			klog.Warningf("%s: throttled, exhausted %d retries: %v", op, throttleMaxRetries, err)
			metrics.ObserveCloudAPI(op, time.Since(start).Seconds(), err)
			return err
		}
		// Otherwise we WILL retry — count it and back off.
		metrics.ObserveThrottleRetry(op)
		jitter := time.Duration(rand.Int63n(int64(delay/2) + 1))
		sleep := delay + jitter
		if sleep > throttleMaxDelay {
			sleep = throttleMaxDelay
		}
		klog.V(2).Infof("%s: throttled (attempt %d/%d), backing off %s", op, attempt+1, throttleMaxRetries, sleep)
		throttleSleep(sleep)
		if delay *= 2; delay > throttleMaxDelay {
			delay = throttleMaxDelay
		}
	}
}
