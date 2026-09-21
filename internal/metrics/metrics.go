// Package metrics exposes the service's Prometheus instrumentation.
//
// The set is small on purpose. A notification relay has three questions worth
// answering from a dashboard: is the queue draining, what is failing, and how
// long does a delivery take. Everything else is a log line.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the collectors.
//
// It is a struct rather than package-level variables so tests can build more
// than one without panicking on duplicate registration.
type Metrics struct {
	registry *prometheus.Registry

	Enqueued     *prometheus.CounterVec
	Delivered    *prometheus.CounterVec
	DeadLettered *prometheus.CounterVec
	Released     *prometheus.CounterVec
	Attempts     *prometheus.CounterVec
	DeliveryTime *prometheus.HistogramVec
	QueueDepth   *prometheus.GaugeVec
	InFlight     prometheus.Gauge
	HTTPRequests *prometheus.CounterVec
	HTTPDuration *prometheus.HistogramVec
}

// New builds and registers the collectors.
func New() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		registry: reg,

		Enqueued: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notifyrelay_enqueued_total",
			Help: "Deliveries accepted into the queue.",
		}, []string{"channel", "channel_type"}),

		Delivered: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notifyrelay_delivered_total",
			Help: "Deliveries the channel accepted.",
		}, []string{"channel", "channel_type"}),

		DeadLettered: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notifyrelay_dead_lettered_total",
			Help: "Deliveries that exhausted their attempts.",
		}, []string{"channel", "channel_type", "reason"}),

		Released: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notifyrelay_released_total",
			Help: "Deliveries returned to the queue without spending an attempt, because no channel could accept them.",
		}, []string{"channel", "channel_type"}),

		Attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notifyrelay_attempts_total",
			Help: "Delivery attempts by outcome class.",
		}, []string{"channel_type", "class"}),

		DeliveryTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "notifyrelay_delivery_seconds",
			Help: "Time one delivery attempt took.",
			// A notification takes milliseconds; anything past ten seconds is
			// a timeout, and the tail beyond that is not worth resolving.
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"channel_type"}),

		QueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "notifyrelay_queue_depth",
			Help: "Deliveries by status.",
		}, []string{"status"}),

		InFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "notifyrelay_in_flight",
			Help: "Deliveries currently being delivered.",
		}),

		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "notifyrelay_http_requests_total",
			Help: "HTTP requests by route and status.",
		}, []string{"method", "route", "status"}),

		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "notifyrelay_http_request_seconds",
			Help:    "HTTP request duration.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		}, []string{"method", "route"}),
	}

	reg.MustRegister(
		m.Enqueued, m.Delivered, m.DeadLettered, m.Released, m.Attempts,
		m.DeliveryTime, m.QueueDepth, m.InFlight,
		m.HTTPRequests, m.HTTPDuration,
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)

	return m
}

// Handler serves the metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// ObserveDelivery records one delivery attempt.
func (m *Metrics) ObserveDelivery(channelType, class string, elapsed time.Duration) {
	if m == nil {
		return
	}
	m.Attempts.WithLabelValues(channelType, class).Inc()
	m.DeliveryTime.WithLabelValues(channelType).Observe(elapsed.Seconds())
}

// SetQueueDepth records a status snapshot.
func (m *Metrics) SetQueueDepth(queued, sending, sent, failed int) {
	if m == nil {
		return
	}
	m.QueueDepth.WithLabelValues("queued").Set(float64(queued))
	m.QueueDepth.WithLabelValues("sending").Set(float64(sending))
	m.QueueDepth.WithLabelValues("sent").Set(float64(sent))
	m.QueueDepth.WithLabelValues("failed").Set(float64(failed))
}
