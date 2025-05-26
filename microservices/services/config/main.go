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

// ConfigService manages configuration for other services
type ConfigService struct {
	configPath string
	metrics    *ServiceMetrics
}

// ServiceMetrics holds Prometheus metrics
type ServiceMetrics struct {
	RequestsTotal prometheus.Counter
}

// NewServiceMetrics creates new metrics
func NewServiceMetrics() *ServiceMetrics {
	return &ServiceMetrics{
		RequestsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "config_service_requests_total",
			Help: "Total number of requests to config service",
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *ServiceMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.RequestsTotal)
}

// NewConfigService creates a new config service
func NewConfigService() (*ConfigService, error) {
	configPath := getEnv("CONFIG_PATH", "/app/config")

	// Initialize metrics
	metrics := NewServiceMetrics()
	metrics.RegisterMetrics()

	return &ConfigService{
		configPath: configPath,
		metrics:    metrics,
	}, nil
}

// HTTP Handlers

func (cs *ConfigService) healthHandler(w http.ResponseWriter, r *http.Request) {
	cs.metrics.RequestsTotal.Inc()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (cs *ConfigService) readyHandler(w http.ResponseWriter, r *http.Request) {
	cs.metrics.RequestsTotal.Inc()
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (cs *ConfigService) statusHandler(w http.ResponseWriter, r *http.Request) {
	cs.metrics.RequestsTotal.Inc()
	status := map[string]interface{}{
		"service":     "config-service",
		"version":     "1.0.0",
		"timestamp":   time.Now().Unix(),
		"config_path": cs.configPath,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (cs *ConfigService) getConfigHandler(w http.ResponseWriter, r *http.Request) {
	cs.metrics.RequestsTotal.Inc()
	
	// Return default configuration
	config := map[string]interface{}{
		"consensus": map[string]interface{}{
			"mode":      "snowman",
			"threshold": 0.67,
		},
		"network": map[string]interface{}{
			"max_peers": 100,
			"timeout":   30,
		},
		"database": map[string]interface{}{
			"host": "localhost",
			"port": 5432,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(config)
}

// setupRoutes configures HTTP routes
func (cs *ConfigService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", cs.healthHandler).Methods("GET")
	router.HandleFunc("/ready", cs.readyHandler).Methods("GET")
	router.HandleFunc("/status", cs.statusHandler).Methods("GET")

	// Config endpoints
	router.HandleFunc("/config", cs.getConfigHandler).Methods("GET")

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
	service, err := NewConfigService()
	if err != nil {
		log.Fatalf("Failed to create config service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8092")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Config Service starting on port %s", port)
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