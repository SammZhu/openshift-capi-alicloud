// Package metrics defines the provider's custom Prometheus metrics and registers
// them with controller-runtime's metrics registry, so they are exposed on the
// manager's metrics endpoint (--metrics-bind-address, default :8080) alongside
// the built-in controller/workqueue/client-go metrics.
//
// These are provider-specific signals the built-in metrics don't cover: how many
// Alibaba Cloud OpenAPI calls we make, their latency and failure rate, how often
// we hit throttling, and ECS instance lifecycle actions (create/adopt/delete/
// harden). They make rate-limit pressure, cloud-side failures, and reconcile cost
// observable in production.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// ResultSuccess / ResultError label cloud-call and instance-op outcomes.
	ResultSuccess = "success"
	ResultError   = "error"
)

var (
	// CloudAPIRequests counts every Alibaba Cloud OpenAPI request issued through
	// retryThrottled, labelled by operation (RunInstances, DescribeInstances, …)
	// and result. A rising error rate flags cloud-side or credential problems.
	CloudAPIRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "capa_cloud_api_requests_total",
		Help: "Total Alibaba Cloud OpenAPI requests issued by the provider, by operation and result.",
	}, []string{"operation", "result"})

	// CloudAPIRequestDuration is the wall-clock latency of a cloud call including
	// any throttle backoff, by operation. Buckets span ~50ms to ~25s.
	CloudAPIRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "capa_cloud_api_request_duration_seconds",
		Help:    "Latency of Alibaba Cloud OpenAPI requests (including throttle backoff), by operation.",
		Buckets: prometheus.ExponentialBuckets(0.05, 2, 10),
	}, []string{"operation"})

	// CloudAPIThrottleRetries counts throttling-induced retries, by operation —
	// the leading indicator of rate-limit pressure (e.g. a large MachineSet scale).
	CloudAPIThrottleRetries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "capa_cloud_api_throttled_retries_total",
		Help: "Alibaba Cloud throttling retries performed by the provider, by operation.",
	}, []string{"operation"})

	// InstanceOps counts ECS instance lifecycle actions, by action
	// (create|adopt|delete|harden) and result — business-level visibility on top
	// of the raw API counters.
	InstanceOps = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "capa_instance_operations_total",
		Help: "ECS instance lifecycle actions performed by the provider, by action and result.",
	}, []string{"action", "result"})
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		CloudAPIRequests,
		CloudAPIRequestDuration,
		CloudAPIThrottleRetries,
		InstanceOps,
	)
}

// result maps an error to the success/error label value.
func result(err error) string {
	if err == nil {
		return ResultSuccess
	}
	return ResultError
}

// ObserveCloudAPI records one cloud call: its latency and its success/error
// result, for the given operation.
func ObserveCloudAPI(operation string, seconds float64, err error) {
	CloudAPIRequestDuration.WithLabelValues(operation).Observe(seconds)
	CloudAPIRequests.WithLabelValues(operation, result(err)).Inc()
}

// ObserveThrottleRetry records one throttling retry for the given operation.
func ObserveThrottleRetry(operation string) {
	CloudAPIThrottleRetries.WithLabelValues(operation).Inc()
}

// ObserveInstanceOp records an ECS lifecycle action and its result.
func ObserveInstanceOp(action string, err error) {
	InstanceOps.WithLabelValues(action, result(err)).Inc()
}
