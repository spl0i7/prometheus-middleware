package prometheusmiddleware

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	dflBuckets = []float64{0.3, 1.0, 2.5, 5.0}
)

const (
	requestName      = "http_requests_total"
	latencyName      = "http_request_duration_seconds"
	responseSizeName = "response_size_bytes"
	requestSizeName  = "request_size_bytes"
)

// Opts specifies options how to create new PrometheusMiddleware.
type Opts struct {
	// Buckets specifies an custom buckets to be used in request histograpm.
	Buckets []float64
	// Subsystem systems have sub-parts that should also be monitored.
	Subsystem string
}

// PrometheusMiddleware specifies the metrics that is going to be generated
type PrometheusMiddleware struct {
	request *prometheus.CounterVec
	latency *prometheus.HistogramVec
	reqSize *prometheus.HistogramVec
	resSize *prometheus.HistogramVec
}

// NewPrometheusMiddleware creates a new PrometheusMiddleware instance
func NewPrometheusMiddleware(opts Opts) *PrometheusMiddleware {
	var prometheusMiddleware PrometheusMiddleware

	counterOpts := prometheus.CounterOpts{
		Name:      requestName,
		Help:      "How many HTTP requests processed, partitioned by status code, method and HTTP path.",
		Subsystem: opts.Subsystem,
	}
	prometheusMiddleware.request = prometheus.NewCounterVec(
		counterOpts,
		[]string{"code", "method", "path"},
	)

	if err := prometheus.Register(prometheusMiddleware.request); err != nil {
		log.Println("prometheusMiddleware.request was not registered:", err)
	}

	buckets := opts.Buckets
	if len(buckets) == 0 {
		buckets = dflBuckets
	}

	histogramOpts := prometheus.HistogramOpts{
		Name:      latencyName,
		Help:      "How long it took to process the request, partitioned by status code, method and HTTP path.",
		Buckets:   buckets,
		Subsystem: opts.Subsystem,
	}
	prometheusMiddleware.latency = prometheus.NewHistogramVec(
		histogramOpts,
		[]string{"code", "method", "path"},
	)

	if err := prometheus.Register(prometheusMiddleware.latency); err != nil {
		log.Println("prometheusMiddleware.latency was not registered:", err)
	}

	reqSizeOpts := prometheus.HistogramOpts{
		Name:    requestSizeName,
		Help:    "How large was the request, partitioned by status code, method and HTTP path.",
		Buckets: buckets,
	}
	prometheusMiddleware.reqSize = prometheus.NewHistogramVec(
		reqSizeOpts,
		[]string{"code", "method", "path"},
	)

	if err := prometheus.Register(prometheusMiddleware.reqSize); err != nil {
		log.Println("prometheusMiddleware.reqSize was not registered:", err)
	}

	resSizeOpts := prometheus.HistogramOpts{
		Name:    responseSizeName,
		Help:    "How large was the response, partitioned by status code, method and HTTP path.",
		Buckets: buckets,
	}
	prometheusMiddleware.resSize = prometheus.NewHistogramVec(
		resSizeOpts,
		[]string{"code", "method", "path"},
	)

	if err := prometheus.Register(prometheusMiddleware.resSize); err != nil {
		log.Println("prometheusMiddleware.resSize was not registered:", err)
	}

	return &prometheusMiddleware
}

// InstrumentHandlerDuration is a middleware that wraps the http.Handler and it record
// how long the handler took to run, which path was called, and the status code.
// This method is going to be used with gorilla/mux.
func (p *PrometheusMiddleware) InstrumentHandlerDuration(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		begin := time.Now()

		delegate := &responseWriterDelegator{ResponseWriter: w}
		rw := delegate

		next.ServeHTTP(rw, r) // call original

		route := mux.CurrentRoute(r)
		path, _ := route.GetPathTemplate()

		code := sanitizeCode(delegate.status)
		method := sanitizeMethod(r.Method)

		p.request.WithLabelValues(
			code,
			method,
			path,
		).Inc()

		p.latency.WithLabelValues(
			code,
			method,
			path,
		).Observe(float64(time.Since(begin)) / float64(time.Second))

		p.reqSize.WithLabelValues(
			code,
			method,
			path,
		).Observe(float64(computeApproximateRequestSize(r)))

		p.resSize.WithLabelValues(
			code,
			method,
			path,
		).Observe(float64(delegate.written))
	})
}

type responseWriterDelegator struct {
	http.ResponseWriter
	status      int
	written     int64
	wroteHeader bool
}

func (r *responseWriterDelegator) WriteHeader(code int) {
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseWriterDelegator) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

func sanitizeMethod(m string) string {
	return strings.ToLower(m)
}

func sanitizeCode(s int) string {
	return strconv.Itoa(s)
}

func computeApproximateRequestSize(r *http.Request) int {
	s := 0
	if r.URL != nil {
		s = len(r.URL.Path)
	}

	s += len(r.Method)
	s += len(r.Proto)
	for name, values := range r.Header {
		s += len(name)
		for _, value := range values {
			s += len(value)
		}
	}
	s += len(r.Host)

	// N.B. r.Form and r.MultipartForm are assumed to be included in r.URL.

	if r.ContentLength != -1 {
		s += int(r.ContentLength)
	}
	return s
}
