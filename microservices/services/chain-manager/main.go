package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ChainManagerService represents the main chain manager service
type ChainManagerService struct {
	db      *gorm.DB
	redis   *redis.Client
	chains  map[string]*Chain
	mu      sync.RWMutex
	metrics *ChainMetrics
}

// Chain represents a blockchain chain
type Chain struct {
	ID          string          `json:"id" gorm:"primaryKey"`
	Name        string          `json:"name"`
	VMID        string          `json:"vm_id"`
	SubnetID    string          `json:"subnet_id"`
	GenesisData json.RawMessage `json:"genesis_data" gorm:"type:jsonb"`
	Status      string          `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// ChainMetrics holds Prometheus metrics
type ChainMetrics struct {
	ChainsTotal       prometheus.Gauge
	ChainOperations   *prometheus.CounterVec
	ChainLatency      prometheus.Histogram
	ChainErrors       prometheus.Counter
}

// NewChainMetrics creates new metrics
func NewChainMetrics() *ChainMetrics {
	return &ChainMetrics{
		ChainsTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "chain_manager_chains_total",
			Help: "Total number of managed chains",
		}),
		ChainOperations: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "chain_manager_operations_total",
				Help: "Total number of chain operations",
			},
			[]string{"operation", "status"},
		),
		ChainLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "chain_manager_operation_duration_seconds",
			Help:    "Time taken for chain operations",
			Buckets: prometheus.DefBuckets,
		}),
		ChainErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "chain_manager_errors_total",
			Help: "Total number of chain manager errors",
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *ChainMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.ChainsTotal)
	prometheus.MustRegister(m.ChainOperations)
	prometheus.MustRegister(m.ChainLatency)
	prometheus.MustRegister(m.ChainErrors)
}

// NewChainManagerService creates a new chain manager service
func NewChainManagerService() (*ChainManagerService, error) {
	// Database connection
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "password")
	dbName := getEnv("DB_NAME", "avalanche_chains")

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		dbHost, dbUser, dbPassword, dbName, dbPort)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	// Auto-migrate tables
	if err := db.AutoMigrate(&Chain{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %v", err)
	}

	// Redis connection
	redisURL := getEnv("REDIS_URL", "redis://localhost:6379")
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %v", err)
	}

	redisClient := redis.NewClient(opt)
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %v", err)
	}

	// Initialize metrics
	metrics := NewChainMetrics()
	metrics.RegisterMetrics()

	service := &ChainManagerService{
		db:      db,
		redis:   redisClient,
		chains:  make(map[string]*Chain),
		metrics: metrics,
	}

	// Load existing chains
	if err := service.loadChains(); err != nil {
		log.Printf("Warning: failed to load chains: %v", err)
	}

	return service, nil
}

// loadChains loads chains from database
func (cms *ChainManagerService) loadChains() error {
	var chains []Chain
	if err := cms.db.Find(&chains).Error; err != nil {
		return err
	}

	cms.mu.Lock()
	defer cms.mu.Unlock()

	for _, chain := range chains {
		cms.chains[chain.ID] = &chain
	}

	cms.metrics.ChainsTotal.Set(float64(len(cms.chains)))
	return nil
}

// CreateChain creates a new blockchain chain
func (cms *ChainManagerService) CreateChain(chain *Chain) error {
	start := time.Now()
	defer func() {
		cms.metrics.ChainLatency.Observe(time.Since(start).Seconds())
	}()

	cms.mu.Lock()
	defer cms.mu.Unlock()

	// Check if chain already exists
	if _, exists := cms.chains[chain.ID]; exists {
		cms.metrics.ChainOperations.WithLabelValues("create", "error").Inc()
		return fmt.Errorf("chain with ID %s already exists", chain.ID)
	}

	// Set timestamps
	chain.CreatedAt = time.Now()
	chain.UpdatedAt = time.Now()
	chain.Status = "created"

	// Save to database
	if err := cms.db.Create(chain).Error; err != nil {
		cms.metrics.ChainOperations.WithLabelValues("create", "error").Inc()
		cms.metrics.ChainErrors.Inc()
		return fmt.Errorf("failed to save chain to database: %v", err)
	}

	// Add to memory
	cms.chains[chain.ID] = chain

	// Update metrics
	cms.metrics.ChainsTotal.Set(float64(len(cms.chains)))
	cms.metrics.ChainOperations.WithLabelValues("create", "success").Inc()

	// Cache in Redis
	chainData, _ := json.Marshal(chain)
	cms.redis.Set(context.Background(), fmt.Sprintf("chain:%s", chain.ID), chainData, time.Hour)

	log.Printf("Created chain: %s", chain.ID)
	return nil
}

// GetChain retrieves a chain by ID
func (cms *ChainManagerService) GetChain(chainID string) (*Chain, error) {
	// Try cache first
	cached, err := cms.redis.Get(context.Background(), fmt.Sprintf("chain:%s", chainID)).Result()
	if err == nil {
		var chain Chain
		if json.Unmarshal([]byte(cached), &chain) == nil {
			return &chain, nil
		}
	}

	cms.mu.RLock()
	chain, exists := cms.chains[chainID]
	cms.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("chain with ID %s not found", chainID)
	}

	return chain, nil
}

// UpdateChainStatus updates the status of a chain
func (cms *ChainManagerService) UpdateChainStatus(chainID, status string) error {
	start := time.Now()
	defer func() {
		cms.metrics.ChainLatency.Observe(time.Since(start).Seconds())
	}()

	cms.mu.Lock()
	defer cms.mu.Unlock()

	chain, exists := cms.chains[chainID]
	if !exists {
		cms.metrics.ChainOperations.WithLabelValues("update", "error").Inc()
		return fmt.Errorf("chain with ID %s not found", chainID)
	}

	chain.Status = status
	chain.UpdatedAt = time.Now()

	// Update in database
	if err := cms.db.Save(chain).Error; err != nil {
		cms.metrics.ChainOperations.WithLabelValues("update", "error").Inc()
		cms.metrics.ChainErrors.Inc()
		return fmt.Errorf("failed to update chain in database: %v", err)
	}

	// Update cache
	chainData, _ := json.Marshal(chain)
	cms.redis.Set(context.Background(), fmt.Sprintf("chain:%s", chainID), chainData, time.Hour)

	cms.metrics.ChainOperations.WithLabelValues("update", "success").Inc()
	log.Printf("Updated chain %s status to %s", chainID, status)
	return nil
}

// ListChains returns all chains
func (cms *ChainManagerService) ListChains() []*Chain {
	cms.mu.RLock()
	defer cms.mu.RUnlock()

	chains := make([]*Chain, 0, len(cms.chains))
	for _, chain := range cms.chains {
		chains = append(chains, chain)
	}

	return chains
}

// HTTP Handlers

func (cms *ChainManagerService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (cms *ChainManagerService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check database connection
	sqlDB, err := cms.db.DB()
	if err != nil || sqlDB.Ping() != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "database unavailable"})
		return
	}

	// Check Redis connection
	if err := cms.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (cms *ChainManagerService) statusHandler(w http.ResponseWriter, r *http.Request) {
	cms.mu.RLock()
	chainCount := len(cms.chains)
	cms.mu.RUnlock()

	status := map[string]interface{}{
		"service":     "chain-manager",
		"chains":      chainCount,
		"timestamp":   time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (cms *ChainManagerService) createChainHandler(w http.ResponseWriter, r *http.Request) {
	var chain Chain
	if err := json.NewDecoder(r.Body).Decode(&chain); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := cms.CreateChain(&chain); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(chain)
}

func (cms *ChainManagerService) getChainHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	chainID := vars["id"]

	chain, err := cms.GetChain(chainID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chain)
}

func (cms *ChainManagerService) listChainsHandler(w http.ResponseWriter, r *http.Request) {
	chains := cms.ListChains()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chains)
}

func (cms *ChainManagerService) updateChainStatusHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	chainID := vars["id"]

	var request struct {
		Status string `json:"status"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := cms.UpdateChainStatus(chainID, request.Status); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// setupRoutes configures HTTP routes
func (cms *ChainManagerService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", cms.healthHandler).Methods("GET")
	router.HandleFunc("/ready", cms.readyHandler).Methods("GET")
	router.HandleFunc("/status", cms.statusHandler).Methods("GET")

	// Chain management endpoints
	router.HandleFunc("/chains", cms.createChainHandler).Methods("POST")
	router.HandleFunc("/chains", cms.listChainsHandler).Methods("GET")
	router.HandleFunc("/chains/{id}", cms.getChainHandler).Methods("GET")
	router.HandleFunc("/chains/{id}/status", cms.updateChainStatusHandler).Methods("PUT")

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
	service, err := NewChainManagerService()
	if err != nil {
		log.Fatalf("Failed to create chain manager service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8082")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Chain Manager Service starting on port %s", port)
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