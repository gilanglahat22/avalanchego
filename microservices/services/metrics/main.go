package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsService aggregates metrics from other services
type MetricsService struct {
	prometheusURL string
	httpClient    *http.Client
	metrics       *ServiceMetrics
}

// ServiceMetrics holds Prometheus metrics
type ServiceMetrics struct {
	RequestsTotal prometheus.Counter
}

// NewServiceMetrics creates new metrics
func NewServiceMetrics() *ServiceMetrics {
	return &ServiceMetrics{
		RequestsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "metrics_service_requests_total",
			Help: "Total number of requests to metrics service",
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *ServiceMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.RequestsTotal)
}

// NewMetricsService creates a new metrics service
func NewMetricsService() (*MetricsService, error) {
	prometheusURL := getEnv("PROMETHEUS_URL", "http://localhost:9090")

	// Initialize metrics
	metrics := NewServiceMetrics()
	metrics.RegisterMetrics()

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	return &MetricsService{
		prometheusURL: prometheusURL,
		httpClient:    httpClient,
		metrics:       metrics,
	}, nil
}

// HTTP Handlers

func (ms *MetricsService) healthHandler(w http.ResponseWriter, r *http.Request) {
	ms.metrics.RequestsTotal.Inc()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (ms *MetricsService) readyHandler(w http.ResponseWriter, r *http.Request) {
	ms.metrics.RequestsTotal.Inc()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (ms *MetricsService) statusHandler(w http.ResponseWriter, r *http.Request) {
	ms.metrics.RequestsTotal.Inc()
	status := map[string]interface{}{
		"service":        "metrics-service",
		"version":        "1.0.0",
		"timestamp":      time.Now().Unix(),
		"prometheus_url": ms.prometheusURL,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// setupRoutes configures HTTP routes
func (ms *MetricsService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", ms.healthHandler).Methods("GET")
	router.HandleFunc("/ready", ms.readyHandler).Methods("GET")
	router.HandleFunc("/status", ms.statusHandler).Methods("GET")

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
	service, err := NewMetricsService()
	if err != nil {
		log.Fatalf("Failed to create metrics service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8091")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Metrics Service starting on port %s", port)
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