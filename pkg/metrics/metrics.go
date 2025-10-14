package metrics

import (
"net/http"
"time"

"github.com/prometheus/client_golang/prometheus"
"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
// Price update metrics
PriceUpdatesTotal = prometheus.NewCounterVec(
prometheus.CounterOpts{
Name: "price_updates_total",
Help: "Total number of price updates received from sources",
},
[]string{"source", "symbol"},
)

PriceStalenessSeconds = prometheus.NewGaugeVec(
prometheus.GaugeOpts{
Name: "price_staleness_seconds",
Help: "Time since last price update for a symbol from a source",
},
[]string{"source", "symbol"},
)

// Aggregation metrics
PriceAggregationDuration = prometheus.NewHistogramVec(
prometheus.HistogramOpts{
Name:    "price_aggregation_duration_seconds",
Help:    "Duration of price aggregation operations",
Buckets: prometheus.DefBuckets,
},
[]string{"method"},
)

OutlierRejectionsTotal = prometheus.NewCounterVec(
prometheus.CounterOpts{
Name: "outlier_rejections_total",
Help: "Total number of outlier prices rejected",
},
[]string{"symbol"},
)

// Source health metrics
SourceHealth = prometheus.NewGaugeVec(
prometheus.GaugeOpts{
Name: "source_health",
Help: "Health status of price sources (1=healthy, 0=unhealthy)",
},
[]string{"source", "type"},
)

SourceLastUpdate = prometheus.NewGaugeVec(
prometheus.GaugeOpts{
Name: "source_last_update_timestamp",
Help: "Unix timestamp of last update from source",
},
[]string{"source"},
)

// HTTP server metrics
HTTPRequestsTotal = prometheus.NewCounterVec(
prometheus.CounterOpts{
Name: "http_requests_total",
Help: "Total number of HTTP requests",
},
[]string{"endpoint", "status"},
)

HTTPRequestDuration = prometheus.NewHistogramVec(
prometheus.HistogramOpts{
Name:    "http_request_duration_seconds",
Help:    "HTTP request latencies",
Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1},
},
[]string{"endpoint"},
)

// Voting metrics
VoteSubmissionsTotal = prometheus.NewCounterVec(
prometheus.CounterOpts{
Name: "vote_submissions_total",
Help: "Total number of vote submissions",
},
[]string{"type", "status"},
)

VotePeriodDuration = prometheus.NewHistogram(
prometheus.HistogramOpts{
Name:    "vote_period_duration_seconds",
Help:    "Duration of voting periods",
Buckets: prometheus.DefBuckets,
},
)

VoteInclusionTime = prometheus.NewHistogram(
prometheus.HistogramOpts{
Name:    "vote_inclusion_time_seconds",
Help:    "Time for vote to be included in block",
Buckets: []float64{1, 2, 5, 10, 20, 30, 60},
},
)

// LCD client metrics
LCDRequestsTotal = prometheus.NewCounterVec(
prometheus.CounterOpts{
Name: "lcd_requests_total",
Help: "Total number of LCD requests",
},
[]string{"endpoint", "status"},
)

LCDFailoversTotal = prometheus.NewCounter(
prometheus.CounterOpts{
Name: "lcd_failovers_total",
Help: "Total number of LCD endpoint failovers",
},
)

// Error metrics
VoteErrorsTotal = prometheus.NewCounterVec(
prometheus.CounterOpts{
Name: "vote_errors_total",
Help: "Total number of voting errors",
},
[]string{"type"},
)
)

// Init initializes Prometheus metrics registry
func Init() {
// Register all metrics
prometheus.MustRegister(
PriceUpdatesTotal,
PriceStalenessSeconds,
PriceAggregationDuration,
OutlierRejectionsTotal,
SourceHealth,
SourceLastUpdate,
HTTPRequestsTotal,
HTTPRequestDuration,
VoteSubmissionsTotal,
VotePeriodDuration,
VoteInclusionTime,
LCDRequestsTotal,
LCDFailoversTotal,
VoteErrorsTotal,
)
}

// ServeHTTP serves Prometheus metrics on the specified address
func ServeHTTP(addr string) error {
http.Handle("/metrics", promhttp.Handler())
return http.ListenAndServe(addr, nil)
}

// RecordSourceUpdate records a price update from a source
func RecordSourceUpdate(source, symbol string) {
PriceUpdatesTotal.WithLabelValues(source, symbol).Inc()
SourceLastUpdate.WithLabelValues(source).SetToCurrentTime()
}

// RecordSourceHealth records the health status of a source
func RecordSourceHealth(source, sourceType string, healthy bool) {
val := 0.0
if healthy {
val = 1.0
}
SourceHealth.WithLabelValues(source, sourceType).Set(val)
}

// RecordAggregation records a price aggregation operation
func RecordAggregation(method string, duration time.Duration) {
PriceAggregationDuration.WithLabelValues(method).Observe(duration.Seconds())
}

// RecordOutlierRejection records an outlier rejection
func RecordOutlierRejection(symbol string) {
OutlierRejectionsTotal.WithLabelValues(symbol).Inc()
}

// RecordHTTPRequest records an HTTP request
func RecordHTTPRequest(endpoint, status string, duration time.Duration) {
HTTPRequestsTotal.WithLabelValues(endpoint, status).Inc()
HTTPRequestDuration.WithLabelValues(endpoint).Observe(duration.Seconds())
}

// RecordVoteSubmission records a vote submission
func RecordVoteSubmission(voteType, status string) {
VoteSubmissionsTotal.WithLabelValues(voteType, status).Inc()
}

// RecordLCDRequest records an LCD request
func RecordLCDRequest(endpoint, status string) {
LCDRequestsTotal.WithLabelValues(endpoint, status).Inc()
}

// RecordLCDFailover records an LCD failover event
func RecordLCDFailover() {
LCDFailoversTotal.Inc()
}

// RecordVoteError records a voting error
func RecordVoteError(errorType string) {
VoteErrorsTotal.WithLabelValues(errorType).Inc()
}
