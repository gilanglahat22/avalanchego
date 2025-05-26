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

// IndexerService handles blockchain data indexing
type IndexerService struct {
	db      *gorm.DB
	redis   *redis.Client
	metrics *IndexerMetrics
	mu      sync.RWMutex
}

// Block represents a blockchain block
type Block struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	Height      int64     `json:"height" gorm:"index"`
	Hash        string    `json:"hash" gorm:"uniqueIndex"`
	ParentHash  string    `json:"parent_hash" gorm:"index"`
	Timestamp   time.Time `json:"timestamp" gorm:"index"`
	TxCount     int       `json:"tx_count"`
	Size        int64     `json:"size"`
	ChainID     string    `json:"chain_id" gorm:"index"`
	Proposer    string    `json:"proposer"`
	GasUsed     int64     `json:"gas_used"`
	GasLimit    int64     `json:"gas_limit"`
	Difficulty  int64     `json:"difficulty"`
	Indexed     bool      `json:"indexed" gorm:"default:false"`
	IndexedAt   time.Time `json:"indexed_at"`
}

// Transaction represents a blockchain transaction
type Transaction struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	Hash        string    `json:"hash" gorm:"uniqueIndex"`
	BlockID     string    `json:"block_id" gorm:"index"`
	BlockHeight int64     `json:"block_height" gorm:"index"`
	From        string    `json:"from" gorm:"index"`
	To          string    `json:"to" gorm:"index"`
	Value       string    `json:"value"`
	Gas         int64     `json:"gas"`
	GasPrice    string    `json:"gas_price"`
	GasUsed     int64     `json:"gas_used"`
	Status      string    `json:"status" gorm:"index"`
	Type        string    `json:"type" gorm:"index"`
	Timestamp   time.Time `json:"timestamp" gorm:"index"`
	Data        []byte    `json:"data"`
	Indexed     bool      `json:"indexed" gorm:"default:false"`
	IndexedAt   time.Time `json:"indexed_at"`
}

// Address represents an indexed address
type Address struct {
	Address       string    `json:"address" gorm:"primaryKey"`
	Balance       string    `json:"balance"`
	TxCount       int64     `json:"tx_count"`
	FirstSeen     time.Time `json:"first_seen"`
	LastActivity  time.Time `json:"last_activity"`
	IsContract    bool      `json:"is_contract"`
	ContractCode  []byte    `json:"contract_code,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// IndexerMetrics holds Prometheus metrics
type IndexerMetrics struct {
	BlocksIndexed     prometheus.Counter
	TransactionsIndexed prometheus.Counter
	AddressesIndexed  prometheus.Counter
	IndexingLatency   prometheus.Histogram
	IndexingErrors    prometheus.Counter
	QueriesTotal      *prometheus.CounterVec
	QueryLatency      prometheus.Histogram
}

// NewIndexerMetrics creates new metrics
func NewIndexerMetrics() *IndexerMetrics {
	return &IndexerMetrics{
		BlocksIndexed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "indexer_blocks_indexed_total",
			Help: "Total number of blocks indexed",
		}),
		TransactionsIndexed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "indexer_transactions_indexed_total",
			Help: "Total number of transactions indexed",
		}),
		AddressesIndexed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "indexer_addresses_indexed_total",
			Help: "Total number of addresses indexed",
		}),
		IndexingLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "indexer_indexing_duration_seconds",
			Help:    "Time taken to index data",
			Buckets: prometheus.DefBuckets,
		}),
		IndexingErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "indexer_errors_total",
			Help: "Total number of indexing errors",
		}),
		QueriesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "indexer_queries_total",
				Help: "Total number of queries",
			},
			[]string{"type", "status"},
		),
		QueryLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "indexer_query_duration_seconds",
			Help:    "Time taken to execute queries",
			Buckets: prometheus.DefBuckets,
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *IndexerMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.BlocksIndexed)
	prometheus.MustRegister(m.TransactionsIndexed)
	prometheus.MustRegister(m.AddressesIndexed)
	prometheus.MustRegister(m.IndexingLatency)
	prometheus.MustRegister(m.IndexingErrors)
	prometheus.MustRegister(m.QueriesTotal)
	prometheus.MustRegister(m.QueryLatency)
}

// NewIndexerService creates a new indexer service
func NewIndexerService() (*IndexerService, error) {
	// Database connection
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "password")
	dbName := getEnv("DB_NAME", "avalanche_indexer")

	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		dbHost, dbUser, dbPassword, dbName, dbPort)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	// Auto-migrate tables
	if err := db.AutoMigrate(&Block{}, &Transaction{}, &Address{}); err != nil {
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
	metrics := NewIndexerMetrics()
	metrics.RegisterMetrics()

	service := &IndexerService{
		db:      db,
		redis:   redisClient,
		metrics: metrics,
	}

	// Start background indexing
	go service.startIndexing()

	return service, nil
}

// IndexBlock indexes a block and its transactions
func (is *IndexerService) IndexBlock(block *Block, transactions []*Transaction) error {
	start := time.Now()
	defer func() {
		is.metrics.IndexingLatency.Observe(time.Since(start).Seconds())
	}()

	is.mu.Lock()
	defer is.mu.Unlock()

	// Start transaction
	tx := is.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Index block
	block.Indexed = true
	block.IndexedAt = time.Now()
	if err := tx.Create(block).Error; err != nil {
		tx.Rollback()
		is.metrics.IndexingErrors.Inc()
		return fmt.Errorf("failed to index block: %v", err)
	}

	// Index transactions
	for _, transaction := range transactions {
		transaction.BlockID = block.ID
		transaction.BlockHeight = block.Height
		transaction.Indexed = true
		transaction.IndexedAt = time.Now()

		if err := tx.Create(transaction).Error; err != nil {
			tx.Rollback()
			is.metrics.IndexingErrors.Inc()
			return fmt.Errorf("failed to index transaction %s: %v", transaction.Hash, err)
		}

		// Update address information
		if err := is.updateAddress(tx, transaction.From, transaction.Timestamp); err != nil {
			log.Printf("Failed to update from address %s: %v", transaction.From, err)
		}
		if transaction.To != "" {
			if err := is.updateAddress(tx, transaction.To, transaction.Timestamp); err != nil {
				log.Printf("Failed to update to address %s: %v", transaction.To, err)
			}
		}
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		is.metrics.IndexingErrors.Inc()
		return fmt.Errorf("failed to commit indexing transaction: %v", err)
	}

	// Update metrics
	is.metrics.BlocksIndexed.Inc()
	is.metrics.TransactionsIndexed.Add(float64(len(transactions)))

	// Cache block data
	blockData, _ := json.Marshal(block)
	is.redis.Set(context.Background(), fmt.Sprintf("block:%s", block.Hash), blockData, time.Hour)
	is.redis.Set(context.Background(), fmt.Sprintf("block:height:%d", block.Height), blockData, time.Hour)

	log.Printf("Indexed block %d with %d transactions", block.Height, len(transactions))
	return nil
}

// updateAddress updates address information
func (is *IndexerService) updateAddress(tx *gorm.DB, addressStr string, timestamp time.Time) error {
	var address Address
	err := tx.Where("address = ?", addressStr).First(&address).Error
	
	if err == gorm.ErrRecordNotFound {
		// Create new address
		address = Address{
			Address:      addressStr,
			Balance:      "0",
			TxCount:      1,
			FirstSeen:    timestamp,
			LastActivity: timestamp,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		if err := tx.Create(&address).Error; err != nil {
			return err
		}
		is.metrics.AddressesIndexed.Inc()
	} else if err == nil {
		// Update existing address
		address.TxCount++
		address.LastActivity = timestamp
		address.UpdatedAt = time.Now()
		if err := tx.Save(&address).Error; err != nil {
			return err
		}
	} else {
		return err
	}

	return nil
}

// GetBlock retrieves a block by hash or height
func (is *IndexerService) GetBlock(identifier string) (*Block, error) {
	start := time.Now()
	defer func() {
		is.metrics.QueryLatency.Observe(time.Since(start).Seconds())
	}()

	// Try cache first
	cached, err := is.redis.Get(context.Background(), fmt.Sprintf("block:%s", identifier)).Result()
	if err == nil {
		var block Block
		if json.Unmarshal([]byte(cached), &block) == nil {
			is.metrics.QueriesTotal.WithLabelValues("block", "cache_hit").Inc()
			return &block, nil
		}
	}

	// Try by height if identifier is numeric
	if height, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		cached, err := is.redis.Get(context.Background(), fmt.Sprintf("block:height:%d", height)).Result()
		if err == nil {
			var block Block
			if json.Unmarshal([]byte(cached), &block) == nil {
				is.metrics.QueriesTotal.WithLabelValues("block", "cache_hit").Inc()
				return &block, nil
			}
		}
	}

	// Query database
	var block Block
	var dbErr error
	
	if height, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		dbErr = is.db.Where("height = ?", height).First(&block).Error
	} else {
		dbErr = is.db.Where("hash = ? OR id = ?", identifier, identifier).First(&block).Error
	}

	if dbErr != nil {
		is.metrics.QueriesTotal.WithLabelValues("block", "error").Inc()
		return nil, fmt.Errorf("block not found: %v", dbErr)
	}

	// Cache result
	blockData, _ := json.Marshal(block)
	is.redis.Set(context.Background(), fmt.Sprintf("block:%s", block.Hash), blockData, time.Hour)
	is.redis.Set(context.Background(), fmt.Sprintf("block:height:%d", block.Height), blockData, time.Hour)

	is.metrics.QueriesTotal.WithLabelValues("block", "success").Inc()
	return &block, nil
}

// GetTransaction retrieves a transaction by hash
func (is *IndexerService) GetTransaction(hash string) (*Transaction, error) {
	start := time.Now()
	defer func() {
		is.metrics.QueryLatency.Observe(time.Since(start).Seconds())
	}()

	// Try cache first
	cached, err := is.redis.Get(context.Background(), fmt.Sprintf("tx:%s", hash)).Result()
	if err == nil {
		var tx Transaction
		if json.Unmarshal([]byte(cached), &tx) == nil {
			is.metrics.QueriesTotal.WithLabelValues("transaction", "cache_hit").Inc()
			return &tx, nil
		}
	}

	// Query database
	var transaction Transaction
	if err := is.db.Where("hash = ?", hash).First(&transaction).Error; err != nil {
		is.metrics.QueriesTotal.WithLabelValues("transaction", "error").Inc()
		return nil, fmt.Errorf("transaction not found: %v", err)
	}

	// Cache result
	txData, _ := json.Marshal(transaction)
	is.redis.Set(context.Background(), fmt.Sprintf("tx:%s", hash), txData, time.Hour)

	is.metrics.QueriesTotal.WithLabelValues("transaction", "success").Inc()
	return &transaction, nil
}

// GetAddress retrieves address information
func (is *IndexerService) GetAddress(addressStr string) (*Address, error) {
	start := time.Now()
	defer func() {
		is.metrics.QueryLatency.Observe(time.Since(start).Seconds())
	}()

	// Try cache first
	cached, err := is.redis.Get(context.Background(), fmt.Sprintf("addr:%s", addressStr)).Result()
	if err == nil {
		var addr Address
		if json.Unmarshal([]byte(cached), &addr) == nil {
			is.metrics.QueriesTotal.WithLabelValues("address", "cache_hit").Inc()
			return &addr, nil
		}
	}

	// Query database
	var address Address
	if err := is.db.Where("address = ?", addressStr).First(&address).Error; err != nil {
		is.metrics.QueriesTotal.WithLabelValues("address", "error").Inc()
		return nil, fmt.Errorf("address not found: %v", err)
	}

	// Cache result
	addrData, _ := json.Marshal(address)
	is.redis.Set(context.Background(), fmt.Sprintf("addr:%s", addressStr), addrData, time.Hour)

	is.metrics.QueriesTotal.WithLabelValues("address", "success").Inc()
	return &address, nil
}

// GetTransactionsByAddress retrieves transactions for an address
func (is *IndexerService) GetTransactionsByAddress(addressStr string, limit, offset int) ([]*Transaction, error) {
	start := time.Now()
	defer func() {
		is.metrics.QueryLatency.Observe(time.Since(start).Seconds())
	}()

	var transactions []*Transaction
	query := is.db.Where("from_addr = ? OR to_addr = ?", addressStr, addressStr).
		Order("timestamp DESC").
		Limit(limit).
		Offset(offset)

	if err := query.Find(&transactions).Error; err != nil {
		is.metrics.QueriesTotal.WithLabelValues("address_transactions", "error").Inc()
		return nil, fmt.Errorf("failed to get transactions: %v", err)
	}

	is.metrics.QueriesTotal.WithLabelValues("address_transactions", "success").Inc()
	return transactions, nil
}

// startIndexing starts the background indexing process
func (is *IndexerService) startIndexing() {
	// Subscribe to new blocks from Redis
	pubsub := is.redis.Subscribe(context.Background(), "new_block")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for msg := range ch {
		var blockData map[string]interface{}
		if err := json.Unmarshal([]byte(msg.Payload), &blockData); err != nil {
			log.Printf("Failed to unmarshal block data: %v", err)
			continue
		}

		// Process the block (this would be more complex in a real implementation)
		log.Printf("Received new block for indexing: %v", blockData)
	}
}

// HTTP Handlers

func (is *IndexerService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (is *IndexerService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check database connection
	sqlDB, err := is.db.DB()
	if err != nil || sqlDB.Ping() != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "database unavailable"})
		return
	}

	// Check Redis connection
	if err := is.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (is *IndexerService) statusHandler(w http.ResponseWriter, r *http.Request) {
	var blockCount, txCount, addrCount int64
	is.db.Model(&Block{}).Count(&blockCount)
	is.db.Model(&Transaction{}).Count(&txCount)
	is.db.Model(&Address{}).Count(&addrCount)

	status := map[string]interface{}{
		"service":      "indexer",
		"blocks":       blockCount,
		"transactions": txCount,
		"addresses":    addrCount,
		"timestamp":    time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (is *IndexerService) getBlockHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	identifier := vars["identifier"]

	block, err := is.GetBlock(identifier)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(block)
}

func (is *IndexerService) getTransactionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	hash := vars["hash"]

	transaction, err := is.GetTransaction(hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(transaction)
}

func (is *IndexerService) getAddressHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	addr, err := is.GetAddress(address)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(addr)
}

func (is *IndexerService) getAddressTransactionsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	address := vars["address"]

	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 50 // default
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	offset := 0 // default
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	transactions, err := is.GetTransactionsByAddress(address, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(transactions)
}

// setupRoutes configures HTTP routes
func (is *IndexerService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", is.healthHandler).Methods("GET")
	router.HandleFunc("/ready", is.readyHandler).Methods("GET")
	router.HandleFunc("/status", is.statusHandler).Methods("GET")

	// Query endpoints
	router.HandleFunc("/blocks/{identifier}", is.getBlockHandler).Methods("GET")
	router.HandleFunc("/transactions/{hash}", is.getTransactionHandler).Methods("GET")
	router.HandleFunc("/addresses/{address}", is.getAddressHandler).Methods("GET")
	router.HandleFunc("/addresses/{address}/transactions", is.getAddressTransactionsHandler).Methods("GET")

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
	service, err := NewIndexerService()
	if err != nil {
		log.Fatalf("Failed to create indexer service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8087")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Indexer Service starting on port %s", port)
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