package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// ValidatorService represents the main validator service
type ValidatorService struct {
	db         *gorm.DB
	redis      *redis.Client
	validators map[string]*Validator
	mu         sync.RWMutex
	metrics    *ValidatorMetrics
}

// Validator represents a network validator
type Validator struct {
	NodeID       string     `json:"node_id" gorm:"primaryKey"`
	PublicKey    string     `json:"public_key"`
	Stake        int64      `json:"stake"`
	StartTime    time.Time  `json:"start_time"`
	EndTime      *time.Time `json:"end_time,omitempty"`
	SubnetID     string     `json:"subnet_id"`
	Active       bool       `json:"active" gorm:"default:true"`
	Weight       int64      `json:"weight"`
	Uptime       float64    `json:"uptime"`
	LastSeen     time.Time  `json:"last_seen"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// StakingTransaction represents a staking transaction
type StakingTransaction struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	ValidatorID string    `json:"validator_id"`
	Type        string    `json:"type"` // "stake", "unstake", "delegate"
	Amount      int64     `json:"amount"`
	Status      string    `json:"status"`
	TxHash      string    `json:"tx_hash"`
	CreatedAt   time.Time `json:"created_at"`
}

// ValidatorMetrics holds Prometheus metrics
type ValidatorMetrics struct {
	ValidatorsTotal     prometheus.Gauge
	ActiveValidators    prometheus.Gauge
	TotalStake          prometheus.Gauge
	ValidatorOperations *prometheus.CounterVec
	ValidatorLatency    prometheus.Histogram
	ValidatorErrors     prometheus.Counter
	UptimeAverage       prometheus.Gauge
}

// NewValidatorMetrics creates new metrics
func NewValidatorMetrics() *ValidatorMetrics {
	return &ValidatorMetrics{
		ValidatorsTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "validator_service_validators_total",
			Help: "Total number of validators",
		}),
		ActiveValidators: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "validator_service_active_validators",
			Help: "Number of active validators",
		}),
		TotalStake: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "validator_service_total_stake",
			Help: "Total stake amount",
		}),
		ValidatorOperations: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "validator_service_operations_total",
				Help: "Total number of validator operations",
			},
			[]string{"operation", "status"},
		),
		ValidatorLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "validator_service_operation_duration_seconds",
			Help:    "Time taken for validator operations",
			Buckets: prometheus.DefBuckets,
		}),
		ValidatorErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "validator_service_errors_total",
			Help: "Total number of validator service errors",
		}),
		UptimeAverage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "validator_service_uptime_average",
			Help: "Average uptime of all validators",
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *ValidatorMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.ValidatorsTotal)
	prometheus.MustRegister(m.ActiveValidators)
	prometheus.MustRegister(m.TotalStake)
	prometheus.MustRegister(m.ValidatorOperations)
	prometheus.MustRegister(m.ValidatorLatency)
	prometheus.MustRegister(m.ValidatorErrors)
	prometheus.MustRegister(m.UptimeAverage)
}

// NewValidatorService creates a new validator service
func NewValidatorService() (*ValidatorService, error) {
	// Database connection
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "password")
	dbName := getEnv("DB_NAME", "avalanche_validators")

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		dbHost, dbUser, dbPassword, dbName, dbPort)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	// Auto-migrate tables
	if err := db.AutoMigrate(&Validator{}, &StakingTransaction{}); err != nil {
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
	metrics := NewValidatorMetrics()
	metrics.RegisterMetrics()

	service := &ValidatorService{
		db:         db,
		redis:      redisClient,
		validators: make(map[string]*Validator),
		metrics:    metrics,
	}

	// Load existing validators
	if err := service.loadValidators(); err != nil {
		log.Printf("Warning: failed to load validators: %v", err)
	}

	// Start background tasks
	go service.updateMetrics()
	go service.monitorValidators()

	return service, nil
}

// loadValidators loads validators from database
func (vs *ValidatorService) loadValidators() error {
	var validators []Validator
	if err := vs.db.Find(&validators).Error; err != nil {
		return err
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	for _, validator := range validators {
		vs.validators[validator.NodeID] = &validator
	}

	return nil
}

// AddValidator adds a new validator
func (vs *ValidatorService) AddValidator(validator *Validator) error {
	start := time.Now()
	defer func() {
		vs.metrics.ValidatorLatency.Observe(time.Since(start).Seconds())
	}()

	vs.mu.Lock()
	defer vs.mu.Unlock()

	// Check if validator already exists
	if _, exists := vs.validators[validator.NodeID]; exists {
		vs.metrics.ValidatorOperations.WithLabelValues("add", "error").Inc()
		return fmt.Errorf("validator with NodeID %s already exists", validator.NodeID)
	}

	// Set default values
	validator.CreatedAt = time.Now()
	validator.UpdatedAt = time.Now()
	validator.LastSeen = time.Now()
	validator.Active = true
	validator.Uptime = 100.0

	// Generate NodeID if not provided
	if validator.NodeID == "" {
		validator.NodeID = generateNodeID()
	}

	// Save to database
	if err := vs.db.Create(validator).Error; err != nil {
		vs.metrics.ValidatorOperations.WithLabelValues("add", "error").Inc()
		vs.metrics.ValidatorErrors.Inc()
		return fmt.Errorf("failed to save validator to database: %v", err)
	}

	// Add to memory
	vs.validators[validator.NodeID] = validator

	// Update metrics
	vs.metrics.ValidatorOperations.WithLabelValues("add", "success").Inc()

	// Cache in Redis
	validatorData, _ := json.Marshal(validator)
	vs.redis.Set(context.Background(), fmt.Sprintf("validator:%s", validator.NodeID), validatorData, time.Hour)

	log.Printf("Added validator: %s", validator.NodeID)
	return nil
}

// GetValidator retrieves a validator by NodeID
func (vs *ValidatorService) GetValidator(nodeID string) (*Validator, error) {
	// Try cache first
	cached, err := vs.redis.Get(context.Background(), fmt.Sprintf("validator:%s", nodeID)).Result()
	if err == nil {
		var validator Validator
		if json.Unmarshal([]byte(cached), &validator) == nil {
			return &validator, nil
		}
	}

	vs.mu.RLock()
	validator, exists := vs.validators[nodeID]
	vs.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("validator with NodeID %s not found", nodeID)
	}

	return validator, nil
}

// UpdateValidatorUptime updates the uptime of a validator
func (vs *ValidatorService) UpdateValidatorUptime(nodeID string, uptime float64) error {
	start := time.Now()
	defer func() {
		vs.metrics.ValidatorLatency.Observe(time.Since(start).Seconds())
	}()

	vs.mu.Lock()
	defer vs.mu.Unlock()

	validator, exists := vs.validators[nodeID]
	if !exists {
		vs.metrics.ValidatorOperations.WithLabelValues("update_uptime", "error").Inc()
		return fmt.Errorf("validator with NodeID %s not found", nodeID)
	}

	validator.Uptime = uptime
	validator.LastSeen = time.Now()
	validator.UpdatedAt = time.Now()

	// Deactivate validator if uptime is too low
	if uptime < 80.0 {
		validator.Active = false
		log.Printf("Deactivated validator %s due to low uptime: %.2f%%", nodeID, uptime)
	}

	// Update in database
	if err := vs.db.Save(validator).Error; err != nil {
		vs.metrics.ValidatorOperations.WithLabelValues("update_uptime", "error").Inc()
		vs.metrics.ValidatorErrors.Inc()
		return fmt.Errorf("failed to update validator in database: %v", err)
	}

	// Update cache
	validatorData, _ := json.Marshal(validator)
	vs.redis.Set(context.Background(), fmt.Sprintf("validator:%s", nodeID), validatorData, time.Hour)

	vs.metrics.ValidatorOperations.WithLabelValues("update_uptime", "success").Inc()
	return nil
}

// RemoveValidator removes a validator
func (vs *ValidatorService) RemoveValidator(nodeID string) error {
	start := time.Now()
	defer func() {
		vs.metrics.ValidatorLatency.Observe(time.Since(start).Seconds())
	}()

	vs.mu.Lock()
	defer vs.mu.Unlock()

	validator, exists := vs.validators[nodeID]
	if !exists {
		vs.metrics.ValidatorOperations.WithLabelValues("remove", "error").Inc()
		return fmt.Errorf("validator with NodeID %s not found", nodeID)
	}

	// Mark as inactive instead of deleting
	validator.Active = false
	endTime := time.Now()
	validator.EndTime = &endTime
	validator.UpdatedAt = time.Now()

	// Update in database
	if err := vs.db.Save(validator).Error; err != nil {
		vs.metrics.ValidatorOperations.WithLabelValues("remove", "error").Inc()
		vs.metrics.ValidatorErrors.Inc()
		return fmt.Errorf("failed to update validator in database: %v", err)
	}

	// Remove from memory
	delete(vs.validators, nodeID)

	// Remove from cache
	vs.redis.Del(context.Background(), fmt.Sprintf("validator:%s", nodeID))

	vs.metrics.ValidatorOperations.WithLabelValues("remove", "success").Inc()
	log.Printf("Removed validator: %s", nodeID)
	return nil
}

// ListValidators returns all validators
func (vs *ValidatorService) ListValidators(activeOnly bool) []*Validator {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	validators := make([]*Validator, 0)
	for _, validator := range vs.validators {
		if !activeOnly || validator.Active {
			validators = append(validators, validator)
		}
	}

	return validators
}

// updateMetrics updates Prometheus metrics
func (vs *ValidatorService) updateMetrics() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		vs.mu.RLock()
		totalValidators := len(vs.validators)
		activeValidators := 0
		totalStake := int64(0)
		totalUptime := 0.0

		for _, validator := range vs.validators {
			if validator.Active {
				activeValidators++
				totalStake += validator.Stake
				totalUptime += validator.Uptime
			}
		}

		avgUptime := 0.0
		if activeValidators > 0 {
			avgUptime = totalUptime / float64(activeValidators)
		}
		vs.mu.RUnlock()

		vs.metrics.ValidatorsTotal.Set(float64(totalValidators))
		vs.metrics.ActiveValidators.Set(float64(activeValidators))
		vs.metrics.TotalStake.Set(float64(totalStake))
		vs.metrics.UptimeAverage.Set(avgUptime)
	}
}

// monitorValidators monitors validator health and uptime
func (vs *ValidatorService) monitorValidators() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		vs.mu.Lock()
		now := time.Now()
		for nodeID, validator := range vs.validators {
			if validator.Active && now.Sub(validator.LastSeen) > 10*time.Minute {
				// Validator hasn't been seen for 10 minutes, reduce uptime
				newUptime := validator.Uptime * 0.95
				validator.Uptime = newUptime
				validator.UpdatedAt = now

				if newUptime < 80.0 {
					validator.Active = false
					log.Printf("Deactivated validator %s due to inactivity", nodeID)
				}

				// Update in database
				vs.db.Save(validator)

				// Update cache
				validatorData, _ := json.Marshal(validator)
				vs.redis.Set(context.Background(), fmt.Sprintf("validator:%s", nodeID), validatorData, time.Hour)
			}
		}
		vs.mu.Unlock()
	}
}

func generateNodeID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// HTTP Handlers

func (vs *ValidatorService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (vs *ValidatorService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check database connection
	sqlDB, err := vs.db.DB()
	if err != nil || sqlDB.Ping() != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "database unavailable"})
		return
	}

	// Check Redis connection
	if err := vs.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (vs *ValidatorService) statusHandler(w http.ResponseWriter, r *http.Request) {
	vs.mu.RLock()
	totalValidators := len(vs.validators)
	activeValidators := 0
	totalStake := int64(0)

	for _, validator := range vs.validators {
		if validator.Active {
			activeValidators++
			totalStake += validator.Stake
		}
	}
	vs.mu.RUnlock()

	status := map[string]interface{}{
		"service":           "validator",
		"total_validators":  totalValidators,
		"active_validators": activeValidators,
		"total_stake":       totalStake,
		"timestamp":         time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (vs *ValidatorService) addValidatorHandler(w http.ResponseWriter, r *http.Request) {
	var validator Validator
	if err := json.NewDecoder(r.Body).Decode(&validator); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := vs.AddValidator(&validator); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(validator)
}

func (vs *ValidatorService) getValidatorHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	nodeID := vars["id"]

	validator, err := vs.GetValidator(nodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(validator)
}

func (vs *ValidatorService) listValidatorsHandler(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("active") == "true"
	validators := vs.ListValidators(activeOnly)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(validators)
}

func (vs *ValidatorService) updateUptimeHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	nodeID := vars["id"]

	var request struct {
		Uptime float64 `json:"uptime"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := vs.UpdateValidatorUptime(nodeID, request.Uptime); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (vs *ValidatorService) removeValidatorHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	nodeID := vars["id"]

	if err := vs.RemoveValidator(nodeID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
}

// setupRoutes configures HTTP routes
func (vs *ValidatorService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", vs.healthHandler).Methods("GET")
	router.HandleFunc("/ready", vs.readyHandler).Methods("GET")
	router.HandleFunc("/status", vs.statusHandler).Methods("GET")

	// Validator management endpoints
	router.HandleFunc("/validators", vs.addValidatorHandler).Methods("POST")
	router.HandleFunc("/validators", vs.listValidatorsHandler).Methods("GET")
	router.HandleFunc("/validators/{id}", vs.getValidatorHandler).Methods("GET")
	router.HandleFunc("/validators/{id}/uptime", vs.updateUptimeHandler).Methods("PUT")
	router.HandleFunc("/validators/{id}", vs.removeValidatorHandler).Methods("DELETE")

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
	service, err := NewValidatorService()
	if err != nil {
		log.Fatalf("Failed to create validator service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8083")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Validator Service starting on port %s", port)
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