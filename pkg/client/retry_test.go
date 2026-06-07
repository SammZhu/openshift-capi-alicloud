package client

import (
	"errors"
	"testing"
	"time"

	sdkerrors "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
)

func throttleErr() error {
	return sdkerrors.NewServerError(400, `{"Code":"Throttling.User","Message":"Request was denied due to user flow control."}`, "")
}

func TestIsThrottlingError(t *testing.T) {
	if !isThrottlingError(throttleErr()) {
		t.Error("Throttling.User should be throttling")
	}
	if !isThrottlingError(sdkerrors.NewServerError(503, `{"Code":"ServiceUnavailable"}`, "")) {
		t.Error("ServiceUnavailable should be throttling")
	}
	if isThrottlingError(sdkerrors.NewServerError(400, `{"Code":"InvalidParameter"}`, "")) {
		t.Error("InvalidParameter should not be throttling")
	}
	if isThrottlingError(nil) || isThrottlingError(errors.New("plain")) {
		t.Error("nil/plain errors are not throttling")
	}
}

func TestRetryThrottled(t *testing.T) {
	// Stub sleep so the test is instant, restore after.
	origSleep, origRetries := throttleSleep, throttleMaxRetries
	var slept int
	throttleSleep = func(time.Duration) { slept++ }
	throttleMaxRetries = 5
	defer func() { throttleSleep, throttleMaxRetries = origSleep, origRetries }()

	// 1. Succeeds after 2 throttles.
	calls := 0
	err := retryThrottled("op", func() error {
		calls++
		if calls < 3 {
			return throttleErr()
		}
		return nil
	})
	if err != nil || calls != 3 || slept != 2 {
		t.Fatalf("retry-then-succeed: err=%v calls=%d slept=%d", err, calls, slept)
	}

	// 2. Non-throttling error returns immediately, no retries.
	calls, slept = 0, 0
	plain := errors.New("boom")
	if err := retryThrottled("op", func() error { calls++; return plain }); !errors.Is(err, plain) || calls != 1 || slept != 0 {
		t.Fatalf("plain error: err=%v calls=%d slept=%d", err, calls, slept)
	}

	// 3. Persistent throttling exhausts retries (1 initial + maxRetries attempts).
	calls, slept = 0, 0
	if err := retryThrottled("op", func() error { calls++; return throttleErr() }); err == nil {
		t.Fatal("persistent throttling should return the error")
	}
	if calls != throttleMaxRetries+1 {
		t.Fatalf("persistent throttling: calls=%d, want %d", calls, throttleMaxRetries+1)
	}
}
