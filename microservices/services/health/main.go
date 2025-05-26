package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HealthService monitors the health of other services
type HealthService struct {
	servicesToCheck []string
	httpClient      *http.Client
	metrics         *HealthMetrics
}

// ServiceStatus represents the status of a service
type ServiceStatus struct {
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Status    string    `json:"status"`
	Latency   int64     `json:"latency_ms"`
	LastCheck time.Time `json:"last_check"`
	Error     string    `json:"error,omitempty"`
}

// HealthMetrics holds Prometheus metrics
type HealthMetrics struct {
	ServiceStatus   *prometheus.GaugeVec
	CheckDuration   prometheus.Histogram
	ChecksTotal     *prometheus.CounterVec
}

// NewHealthMetrics creates new metrics
func NewHealthMetrics() *HealthMetrics {
	return &HealthMetrics{
		ServiceStatus: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "health_service_status",
				Help: "Status of monitored services (1=healthy, 0=unhealthy)",
			},
			[]string{"service"},
		),
		CheckDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "health_check_duration_seconds",
			Help:    "Duration of health checks",
			Buckets: prometheus.DefBuckets,
		}),
		ChecksTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "health_checks_total",
				Help: "Total number of health checks",
			},
			[]string{"service", "status"},
		),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *HealthMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.ServiceStatus)
	prometheus.MustRegister(m.CheckDuration)
	prometheus.MustRegister(m.ChecksTotal)
}

// NewHealthService creates a new health service
func NewHealthService() (*HealthService, error) {
	// Parse services to check from environment
	servicesToCheckStr := getEnv("SERVICES_TO_CHECK", "consensus-service:8080,vm-manager-service:8081,chain-manager-service:8082")
	servicesToCheck := strings.Split(servicesToCheckStr, ",")

	// Initialize metrics
	metrics := NewHealthMetrics()
	metrics.RegisterMetrics()

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	service := &HealthService{
		servicesToCheck: servicesToCheck,
		httpClient:      httpClient,
		metrics:         metrics,
	}

	// Start health checking
	go service.startHealthChecking()

	return service, nil
}

// checkService checks the health of a single service
func (hs *HealthService) checkService(serviceURL string) *ServiceStatus {
	start := time.Now()
	defer func() {
		hs.metrics.CheckDuration.Observe(time.Since(start).Seconds())
	}()

	// Extract service name from URL
	serviceName := strings.Split(serviceURL, ":")[0]
	
	status := &ServiceStatus{
		Name:      serviceName,
		URL:       "http://" + serviceURL + "/health",
		LastCheck: time.Now(),
	}

	resp, err := hs.httpClient.Get(status.URL)
	if err != nil {
		status.Status = "unhealthy"
		status.Error = err.Error()
		hs.metrics.ServiceStatus.WithLabelValues(serviceName).Set(0)
		hs.metrics.ChecksTotal.WithLabelValues(serviceName, "error").Inc()
		return status
	}
	defer resp.Body.Close()

	status.Latency = time.Since(start).Milliseconds()

	if resp.StatusCode == http.StatusOK {
		status.Status = "healthy"
		hs.metrics.ServiceStatus.WithLabelValues(serviceName).Set(1)
		hs.metrics.ChecksTotal.WithLabelValues(serviceName, "success").Inc()
	} else {
		status.Status = "unhealthy"
		status.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		hs.metrics.ServiceStatus.WithLabelValues(serviceName).Set(0)
		hs.metrics.ChecksTotal.WithLabelValues(serviceName, "error").Inc()
	}

	return status
}

// startHealthChecking starts the health checking loop
func (hs *HealthService) startHealthChecking() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		for _, service := range hs.servicesToCheck {
			go func(svc string) {
				status := hs.checkService(svc)
				log.Printf("Health check: %s - %s (latency: %dms)", status.Name, status.Status, status.Latency)
			}(service)
		}
	}
}

// HTTP Handlers

func (hs *HealthService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (hs *HealthService) readyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (hs *HealthService) statusHandler(w http.ResponseWriter, r *http.Request) {
	statuses := make([]*ServiceStatus, 0, len(hs.servicesToCheck))
	
	for _, service := range hs.servicesToCheck {
		status := hs.checkService(service)
		statuses = append(statuses, status)
	}

	allHealthy := true
	for _, status := range statuses {
		if status.Status != "healthy" {
			allHealthy = false
			break
		}
	}

	response := map[string]interface{}{
		"overall_status": map[string]bool{"healthy": allHealthy},
		"services":       statuses,
		"timestamp":      time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// setupRoutes configures HTTP routes
func (hs *HealthService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", hs.healthHandler).Methods("GET")
	router.HandleFunc("/ready", hs.readyHandler).Methods("GET")
	router.HandleFunc("/status", hs.statusHandler).Methods("GET")

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
	service, err := NewHealthService()
	if err != nil {
		log.Fatalf("Failed to create health service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8090")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Health Service starting on port %s", port)
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