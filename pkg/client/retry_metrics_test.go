package client

import (
	"errors"
	"testing"
	"time"

	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/SammZhu/openshift-capi-alicloud/pkg/metrics"
)

// retryThrottled is the single chokepoint every cloud call flows through, so
// asserting it feeds the metrics covers the whole instrumentation surface.
func TestRetryThrottled_RecordsMetrics(t *testing.T) {
	const opOK = "MetricsTestOK"
	before := testutil.ToFloat64(metrics.CloudAPIRequests.WithLabelValues(opOK, metrics.ResultSuccess))
	if err := retryThrottled(opOK, func() error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := testutil.ToFloat64(metrics.CloudAPIRequests.WithLabelValues(opOK, metrics.ResultSuccess)); got != before+1 {
		t.Errorf("success counter = %v, want %v", got, before+1)
	}
	if got := testutil.CollectAndCount(metrics.CloudAPIRequestDuration); got == 0 {
		t.Errorf("expected a duration observation to be recorded")
	}

	const opErr = "MetricsTestErr"
	beforeErr := testutil.ToFloat64(metrics.CloudAPIRequests.WithLabelValues(opErr, metrics.ResultError))
	if err := retryThrottled(opErr, func() error { return errors.New("boom") }); err == nil {
		t.Fatal("expected error")
	}
	if got := testutil.ToFloat64(metrics.CloudAPIRequests.WithLabelValues(opErr, metrics.ResultError)); got != beforeErr+1 {
		t.Errorf("error counter = %v, want %v", got, beforeErr+1)
	}
}

func TestRetryThrottled_RecordsThrottleRetries(t *testing.T) {
	// Shrink the retry budget + neutralise sleeps so the test is fast.
	defer func(r int, s func(time.Duration)) { throttleMaxRetries, throttleSleep = r, s }(throttleMaxRetries, throttleSleep)
	throttleMaxRetries = 2
	throttleSleep = func(time.Duration) {}

	const op = "MetricsTestThrottle"
	before := testutil.ToFloat64(metrics.CloudAPIThrottleRetries.WithLabelValues(op))
	// A Throttling* code makes isThrottlingError true; always-throttle exhausts.
	throttled := sdkerrors.NewServerError(429, `{"Code":"Throttling"}`, "")
	err := retryThrottled(op, func() error { return throttled })
	if err == nil {
		t.Fatal("expected the throttling error to surface after exhaustion")
	}
	// throttleMaxRetries=2 → retries recorded on attempts 0 and 1 before giving up.
	if got := testutil.ToFloat64(metrics.CloudAPIThrottleRetries.WithLabelValues(op)); got != before+2 {
		t.Errorf("throttle-retry counter = %v, want %v", got, before+2)
	}
}

func TestObserveInstanceOp(t *testing.T) {
	before := testutil.ToFloat64(metrics.InstanceOps.WithLabelValues("create", metrics.ResultSuccess))
	metrics.ObserveInstanceOp("create", nil)
	if got := testutil.ToFloat64(metrics.InstanceOps.WithLabelValues("create", metrics.ResultSuccess)); got != before+1 {
		t.Errorf("instance-op counter = %v, want %v", got, before+1)
	}
}
