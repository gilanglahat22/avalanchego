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

// ConsensusService represents the main consensus service
type ConsensusService struct {
	db                *gorm.DB
	redis             *redis.Client
	consensusMode     string
	validatorThreshold float64
	blockHeight       int64
	validators        map[string]*Validator
	mu                sync.RWMutex
	metrics           *ConsensusMetrics
}

// Validator represents a network validator
type Validator struct {
	NodeID    string    `json:"node_id" gorm:"primaryKey"`
	Stake     int64     `json:"stake"`
	StartTime time.Time `json:"start_time"`
	EndTime   *time.Time `json:"end_time,omitempty"`
	SubnetID  string    `json:"subnet_id"`
	Active    bool      `json:"active" gorm:"default:true"`
	CreatedAt time.Time `json:"created_at"`
}

// Block represents a blockchain block
type Block struct {
	ID        string          `json:"id" gorm:"primaryKey"`
	ParentID  string          `json:"parent_id"`
	Height    int64           `json:"height"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data" gorm:"type:jsonb"`
	CreatedAt time.Time       `json:"created_at"`
}

// ConsensusMetrics holds Prometheus metrics
type ConsensusMetrics struct {
	BlocksProcessed    prometheus.Counter
	BlocksProduced     prometheus.Counter
	ConsensusLatency   prometheus.Histogram
	ValidatorCount     prometheus.Gauge
	BlockHeight        prometheus.Gauge
	ConsensusErrors    prometheus.Counter
}

// NewConsensusMetrics creates new metrics
func NewConsensusMetrics() *ConsensusMetrics {
	return &ConsensusMetrics{
		BlocksProcessed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "consensus_blocks_processed_total",
			Help: "Total number of blocks processed",
		}),
		BlocksProduced: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "consensus_blocks_produced_total",
			Help: "Total number of blocks produced",
		}),
		ConsensusLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "consensus_block_processing_duration_seconds",
			Help:    "Time taken to process blocks",
			Buckets: prometheus.DefBuckets,
		}),
		ValidatorCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "consensus_active_validators",
			Help: "Number of active validators",
		}),
		BlockHeight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "consensus_block_height",
			Help: "Current block height",
		}),
		ConsensusErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "consensus_errors_total",
			Help: "Total number of consensus errors",
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *ConsensusMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.BlocksProcessed)
	prometheus.MustRegister(m.BlocksProduced)
	prometheus.MustRegister(m.ConsensusLatency)
	prometheus.MustRegister(m.ValidatorCount)
	prometheus.MustRegister(m.BlockHeight)
	prometheus.MustRegister(m.ConsensusErrors)
}

// NewConsensusService creates a new consensus service
func NewConsensusService() (*ConsensusService, error) {
	// Database connection
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "password")
	dbName := getEnv("DB_NAME", "avalanche_state")

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		dbHost, dbUser, dbPassword, dbName, dbPort)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	// Auto-migrate tables
	if err := db.AutoMigrate(&Validator{}, &Block{}); err != nil {
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

	// Parse configuration
	consensusMode := getEnv("CONSENSUS_MODE", "snowman")
	thresholdStr := getEnv("VALIDATOR_THRESHOLD", "0.67")
	threshold, err := strconv.ParseFloat(thresholdStr, 64)
	if err != nil {
		threshold = 0.67
	}

	// Initialize metrics
	metrics := NewConsensusMetrics()
	metrics.RegisterMetrics()

	service := &ConsensusService{
		db:                 db,
		redis:              redisClient,
		consensusMode:      consensusMode,
		validatorThreshold: threshold,
		blockHeight:        0,
		validators:         make(map[string]*Validator),
		metrics:            metrics,
	}

	// Load existing validators
	if err := service.loadValidators(); err != nil {
		log.Printf("Warning: failed to load validators: %v", err)
	}

	return service, nil
}

// loadValidators loads validators from database
func (cs *ConsensusService) loadValidators() error {
	var validators []Validator
	if err := cs.db.Where("active = ?", true).Find(&validators).Error; err != nil {
		return err
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	for _, validator := range validators {
		cs.validators[validator.NodeID] = &validator
	}

	cs.metrics.ValidatorCount.Set(float64(len(cs.validators)))
	return nil
}

// ProcessBlock processes a new block through consensus
func (cs *ConsensusService) ProcessBlock(block *Block) error {
	start := time.Now()
	defer func() {
		cs.metrics.ConsensusLatency.Observe(time.Since(start).Seconds())
		cs.metrics.BlocksProcessed.Inc()
	}()

	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Validate block
	if err := cs.validateBlock(block); err != nil {
		cs.metrics.ConsensusErrors.Inc()
		return fmt.Errorf("block validation failed: %v", err)
	}

	// Run consensus algorithm
	if err := cs.runConsensus(block); err != nil {
		cs.metrics.ConsensusErrors.Inc()
		return fmt.Errorf("consensus failed: %v", err)
	}

	// Store block
	if err := cs.db.Create(block).Error; err != nil {
		cs.metrics.ConsensusErrors.Inc()
		return fmt.Errorf("failed to store block: %v", err)
	}

	// Update block height
	if block.Height > cs.blockHeight {
		cs.blockHeight = block.Height
		cs.metrics.BlockHeight.Set(float64(cs.blockHeight))
	}

	// Publish block to Redis
	blockData, _ := json.Marshal(block)
	cs.redis.Publish(context.Background(), "new_block", blockData)

	cs.metrics.BlocksProduced.Inc()
	log.Printf("Block %s processed successfully at height %d", block.ID, block.Height)

	return nil
}

// validateBlock validates a block
func (cs *ConsensusService) validateBlock(block *Block) error {
	if block.ID == "" {
		return fmt.Errorf("block ID cannot be empty")
	}

	if block.Height <= 0 {
		return fmt.Errorf("block height must be positive")
	}

	if block.Timestamp.IsZero() {
		return fmt.Errorf("block timestamp cannot be zero")
	}

	// Check if block already exists
	var existingBlock Block
	if err := cs.db.Where("id = ?", block.ID).First(&existingBlock).Error; err == nil {
		return fmt.Errorf("block %s already exists", block.ID)
	}

	return nil
}

// runConsensus runs the consensus algorithm
func (cs *ConsensusService) runConsensus(block *Block) error {
	switch cs.consensusMode {
	case "snowman":
		return cs.runSnowmanConsensus(block)
	case "avalanche":
		return cs.runAvalancheConsensus(block)
	default:
		return fmt.Errorf("unknown consensus mode: %s", cs.consensusMode)
	}
}

// runSnowmanConsensus runs Snowman consensus
func (cs *ConsensusService) runSnowmanConsensus(block *Block) error {
	// Simplified Snowman consensus implementation
	activeValidators := 0
	for _, validator := range cs.validators {
		if validator.Active {
			activeValidators++
		}
	}

	if activeValidators == 0 {
		return fmt.Errorf("no active validators")
	}

	// Simulate consensus voting
	requiredVotes := int(float64(activeValidators) * cs.validatorThreshold)
	votes := activeValidators // Simplified: assume all validators vote yes

	if votes < requiredVotes {
		return fmt.Errorf("insufficient votes: got %d, required %d", votes, requiredVotes)
	}

	return nil
}

// runAvalancheConsensus runs Avalanche consensus
func (cs *ConsensusService) runAvalancheConsensus(block *Block) error {
	// Simplified Avalanche consensus implementation
	return cs.runSnowmanConsensus(block) // For now, use same logic
}

// AddValidator adds a new validator
func (cs *ConsensusService) AddValidator(validator *Validator) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	validator.CreatedAt = time.Now()
	validator.Active = true

	if err := cs.db.Create(validator).Error; err != nil {
		return fmt.Errorf("failed to add validator: %v", err)
	}

	cs.validators[validator.NodeID] = validator
	cs.metrics.ValidatorCount.Set(float64(len(cs.validators)))

	log.Printf("Validator %s added with stake %d", validator.NodeID, validator.Stake)
	return nil
}

// RemoveValidator removes a validator
func (cs *ConsensusService) RemoveValidator(nodeID string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, exists := cs.validators[nodeID]; !exists {
		return fmt.Errorf("validator %s not found", nodeID)
	}

	// Mark as inactive instead of deleting
	if err := cs.db.Model(&Validator{}).Where("node_id = ?", nodeID).Update("active", false).Error; err != nil {
		return fmt.Errorf("failed to remove validator: %v", err)
	}

	delete(cs.validators, nodeID)
	cs.metrics.ValidatorCount.Set(float64(len(cs.validators)))

	log.Printf("Validator %s removed", nodeID)
	return nil
}

// HTTP Handlers

func (cs *ConsensusService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (cs *ConsensusService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check database connection
	sqlDB, err := cs.db.DB()
	if err != nil || sqlDB.Ping() != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "database unavailable"})
		return
	}

	// Check Redis connection
	if err := cs.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (cs *ConsensusService) statusHandler(w http.ResponseWriter, r *http.Request) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	status := map[string]interface{}{
		"consensus_mode":      cs.consensusMode,
		"validator_threshold": cs.validatorThreshold,
		"block_height":        cs.blockHeight,
		"active_validators":   len(cs.validators),
		"timestamp":           time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (cs *ConsensusService) processBlockHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var block Block
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid block data"})
		return
	}

	if err := cs.ProcessBlock(&block); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "block processed", "block_id": block.ID})
}

func (cs *ConsensusService) addValidatorHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var validator Validator
	if err := json.NewDecoder(r.Body).Decode(&validator); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid validator data"})
		return
	}

	if err := cs.AddValidator(&validator); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "validator added", "node_id": validator.NodeID})
}

func (cs *ConsensusService) getValidatorsHandler(w http.ResponseWriter, r *http.Request) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	validators := make([]*Validator, 0, len(cs.validators))
	for _, validator := range cs.validators {
		validators = append(validators, validator)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(validators)
}

// setupRoutes sets up HTTP routes
func (cs *ConsensusService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health checks
	router.HandleFunc("/health", cs.healthHandler).Methods("GET")
	router.HandleFunc("/ready", cs.readyHandler).Methods("GET")
	router.HandleFunc("/startup", cs.healthHandler).Methods("GET")

	// API endpoints
	router.HandleFunc("/status", cs.statusHandler).Methods("GET")
	router.HandleFunc("/block", cs.processBlockHandler).Methods("POST")
	router.HandleFunc("/validators", cs.addValidatorHandler).Methods("POST")
	router.HandleFunc("/validators", cs.getValidatorsHandler).Methods("GET")

	// Metrics
	router.Handle("/metrics", promhttp.Handler())

	return router
}

// getEnv gets environment variable with default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	log.Println("Starting Consensus Service...")

	// Create consensus service
	service, err := NewConsensusService()
	if err != nil {
		log.Fatalf("Failed to create consensus service: %v", err)
	}

	// Setup HTTP server
	router := service.setupRoutes()
	server := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Println("Consensus Service listening on :8080")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down Consensus Service...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("Consensus Service stopped")
} 