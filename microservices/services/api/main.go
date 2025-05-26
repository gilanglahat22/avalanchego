package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// APIService provides a unified API interface
type APIService struct {
	consensusURL    string
	vmManagerURL    string
	chainManagerURL string
	validatorURL    string
	metrics         *APIMetrics
	httpClient      *http.Client
}

// APIMetrics holds Prometheus metrics
type APIMetrics struct {
	RequestsTotal    *prometheus.CounterVec
	RequestDuration  prometheus.Histogram
	ProxyRequests    *prometheus.CounterVec
	ServiceErrors    *prometheus.CounterVec
}

// NewAPIMetrics creates new metrics
func NewAPIMetrics() *APIMetrics {
	return &APIMetrics{
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "api_requests_total",
				Help: "Total number of API requests",
			},
			[]string{"method", "endpoint", "status"},
		),
		RequestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "api_request_duration_seconds",
			Help:    "API request duration",
			Buckets: prometheus.DefBuckets,
		}),
		ProxyRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "api_proxy_requests_total",
				Help: "Total number of proxy requests to backend services",
			},
			[]string{"service", "status"},
		),
		ServiceErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "api_service_errors_total",
				Help: "Total number of service errors",
			},
			[]string{"service", "type"},
		),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *APIMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.RequestsTotal)
	prometheus.MustRegister(m.RequestDuration)
	prometheus.MustRegister(m.ProxyRequests)
	prometheus.MustRegister(m.ServiceErrors)
}

// NewAPIService creates a new API service
func NewAPIService() (*APIService, error) {
	// Get service URLs from environment
	consensusURL := getEnv("CONSENSUS_SERVICE_URL", "http://localhost:8080")
	vmManagerURL := getEnv("VM_MANAGER_URL", "http://localhost:8081")
	chainManagerURL := getEnv("CHAIN_MANAGER_URL", "http://localhost:8082")
	validatorURL := getEnv("VALIDATOR_SERVICE_URL", "http://localhost:8083")

	// Initialize metrics
	metrics := NewAPIMetrics()
	metrics.RegisterMetrics()

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	return &APIService{
		consensusURL:    consensusURL,
		vmManagerURL:    vmManagerURL,
		chainManagerURL: chainManagerURL,
		validatorURL:    validatorURL,
		metrics:         metrics,
		httpClient:      httpClient,
	}, nil
}

// proxyRequest proxies a request to a backend service
func (as *APIService) proxyRequest(serviceName, serviceURL string, w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		as.metrics.RequestDuration.Observe(time.Since(start).Seconds())
	}()

	// Build target URL
	targetURL := serviceURL + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// Create new request
	var body io.Reader
	if r.Body != nil {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			as.metrics.ServiceErrors.WithLabelValues(serviceName, "read_body").Inc()
			http.Error(w, "Failed to read request body", http.StatusInternalServerError)
			return
		}
		body = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest(r.Method, targetURL, body)
	if err != nil {
		as.metrics.ServiceErrors.WithLabelValues(serviceName, "create_request").Inc()
		http.Error(w, "Failed to create request", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Make request
	resp, err := as.httpClient.Do(req)
	if err != nil {
		as.metrics.ServiceErrors.WithLabelValues(serviceName, "request_failed").Inc()
		as.metrics.ProxyRequests.WithLabelValues(serviceName, "error").Inc()
		http.Error(w, fmt.Sprintf("Service unavailable: %v", err), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Set status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	if _, err := io.Copy(w, resp.Body); err != nil {
		as.metrics.ServiceErrors.WithLabelValues(serviceName, "copy_response").Inc()
		log.Printf("Failed to copy response body: %v", err)
	}

	// Record metrics
	status := "success"
	if resp.StatusCode >= 400 {
		status = "error"
	}
	as.metrics.ProxyRequests.WithLabelValues(serviceName, status).Inc()
}

// Health and status handlers
func (as *APIService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (as *APIService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check if backend services are reachable
	services := map[string]string{
		"consensus":     as.consensusURL + "/health",
		"vm-manager":    as.vmManagerURL + "/health",
		"chain-manager": as.chainManagerURL + "/health",
		"validator":     as.validatorURL + "/health",
	}

	allReady := true
	serviceStatus := make(map[string]string)

	for name, url := range services {
		resp, err := as.httpClient.Get(url)
		if err != nil || resp.StatusCode != http.StatusOK {
			allReady = false
			serviceStatus[name] = "not ready"
		} else {
			serviceStatus[name] = "ready"
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	status := http.StatusOK
	if !allReady {
		status = http.StatusServiceUnavailable
	}

	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   map[string]string{"ready": fmt.Sprintf("%t", allReady)},
		"services": serviceStatus,
	})
}

func (as *APIService) statusHandler(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"service":     "api-service",
		"version":     "1.0.0",
		"timestamp":   time.Now().Unix(),
		"uptime":      time.Since(time.Now()).String(), // This would be calculated from start time
		"endpoints": map[string]string{
			"consensus":     as.consensusURL,
			"vm-manager":    as.vmManagerURL,
			"chain-manager": as.chainManagerURL,
			"validator":     as.validatorURL,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// API endpoint handlers
func (as *APIService) getBlockchainInfoHandler(w http.ResponseWriter, r *http.Request) {
	// Aggregate information from multiple services
	info := make(map[string]interface{})

	// Get consensus info
	if resp, err := as.httpClient.Get(as.consensusURL + "/status"); err == nil {
		var consensusInfo map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&consensusInfo); err == nil {
			info["consensus"] = consensusInfo
		}
		resp.Body.Close()
	}

	// Get chain manager info
	if resp, err := as.httpClient.Get(as.chainManagerURL + "/status"); err == nil {
		var chainInfo map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&chainInfo); err == nil {
			info["chains"] = chainInfo
		}
		resp.Body.Close()
	}

	// Get validator info
	if resp, err := as.httpClient.Get(as.validatorURL + "/status"); err == nil {
		var validatorInfo map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&validatorInfo); err == nil {
			info["validators"] = validatorInfo
		}
		resp.Body.Close()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// Middleware for request logging and metrics
func (as *APIService) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response writer wrapper to capture status code
		wrapper := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Process request
		next.ServeHTTP(wrapper, r)

		// Log and record metrics
		duration := time.Since(start)
		log.Printf("%s %s %d %v", r.Method, r.URL.Path, wrapper.statusCode, duration)

		as.metrics.RequestsTotal.WithLabelValues(
			r.Method,
			r.URL.Path,
			strconv.Itoa(wrapper.statusCode),
		).Inc()
	})
}

// responseWriter wrapper to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// setupRoutes configures HTTP routes
func (as *APIService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Apply logging middleware
	router.Use(as.loggingMiddleware)

	// Health endpoints
	router.HandleFunc("/health", as.healthHandler).Methods("GET")
	router.HandleFunc("/ready", as.readyHandler).Methods("GET")
	router.HandleFunc("/status", as.statusHandler).Methods("GET")

	// Unified API endpoints
	router.HandleFunc("/api/v1/info", as.getBlockchainInfoHandler).Methods("GET")

	// Proxy routes to backend services
	consensusRouter := router.PathPrefix("/api/v1/consensus").Subrouter()
	consensusRouter.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Remove the /api/v1/consensus prefix
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/v1/consensus")
		as.proxyRequest("consensus", as.consensusURL, w, r)
	})

	vmRouter := router.PathPrefix("/api/v1/vms").Subrouter()
	vmRouter.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/v1/vms")
		as.proxyRequest("vm-manager", as.vmManagerURL, w, r)
	})

	chainRouter := router.PathPrefix("/api/v1/chains").Subrouter()
	chainRouter.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/v1/chains")
		as.proxyRequest("chain-manager", as.chainManagerURL, w, r)
	})

	validatorRouter := router.PathPrefix("/api/v1/validators").Subrouter()
	validatorRouter.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/v1/validators")
		as.proxyRequest("validator", as.validatorURL, w, r)
	})

	// Metrics endpoint
	router.Handle("/metrics", promhttp.Handler())

	return router
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	service, err := NewAPIService()
	if err != nil {
		log.Fatalf("Failed to create API service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8089")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("API Service starting on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
} 