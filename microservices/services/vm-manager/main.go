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

// VMManagerService represents the main VM manager service
type VMManagerService struct {
	db          *gorm.DB
	redis       *redis.Client
	maxVMs      int
	vms         map[string]*VMInstance
	mu          sync.RWMutex
	metrics     *VMMetrics
}

// VMInstance represents a virtual machine instance
type VMInstance struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	ChainID   string    `json:"chain_id"`
	VMType    string    `json:"vm_type"`
	Status    string    `json:"status" gorm:"default:'stopped'"`
	Config    string    `json:"config" gorm:"type:text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
}

// VMMetrics holds Prometheus metrics
type VMMetrics struct {
	VMsTotal        prometheus.Gauge
	VMsRunning      prometheus.Gauge
	VMsStarted      prometheus.Counter
	VMsStopped      prometheus.Counter
	VMErrors        prometheus.Counter
	VMStartDuration prometheus.Histogram
	VMStopDuration  prometheus.Histogram
}

// NewVMMetrics creates new metrics
func NewVMMetrics() *VMMetrics {
	return &VMMetrics{
		VMsTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vm_manager_vms_total",
			Help: "Total number of VM instances",
		}),
		VMsRunning: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "vm_manager_vms_running",
			Help: "Number of running VM instances",
		}),
		VMsStarted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "vm_manager_vms_started_total",
			Help: "Total number of VMs started",
		}),
		VMsStopped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "vm_manager_vms_stopped_total",
			Help: "Total number of VMs stopped",
		}),
		VMErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "vm_manager_errors_total",
			Help: "Total number of VM manager errors",
		}),
		VMStartDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "vm_manager_start_duration_seconds",
			Help:    "Time taken to start VMs",
			Buckets: prometheus.DefBuckets,
		}),
		VMStopDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "vm_manager_stop_duration_seconds",
			Help:    "Time taken to stop VMs",
			Buckets: prometheus.DefBuckets,
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *VMMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.VMsTotal)
	prometheus.MustRegister(m.VMsRunning)
	prometheus.MustRegister(m.VMsStarted)
	prometheus.MustRegister(m.VMsStopped)
	prometheus.MustRegister(m.VMErrors)
	prometheus.MustRegister(m.VMStartDuration)
	prometheus.MustRegister(m.VMStopDuration)
}

// NewVMManagerService creates a new VM manager service
func NewVMManagerService() (*VMManagerService, error) {
	// Database connection
	dbURL := getEnv("STATE_DB_URL", "postgresql://postgres:password@localhost:5432/avalanche_state")
	
	db, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	// Auto-migrate tables
	if err := db.AutoMigrate(&VMInstance{}); err != nil {
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
	maxVMsStr := getEnv("MAX_VMS_PER_NODE", "10")
	maxVMs, err := strconv.Atoi(maxVMsStr)
	if err != nil {
		maxVMs = 10
	}

	// Initialize metrics
	metrics := NewVMMetrics()
	metrics.RegisterMetrics()

	service := &VMManagerService{
		db:      db,
		redis:   redisClient,
		maxVMs:  maxVMs,
		vms:     make(map[string]*VMInstance),
		metrics: metrics,
	}

	// Load existing VMs
	if err := service.loadVMs(); err != nil {
		log.Printf("Warning: failed to load VMs: %v", err)
	}

	return service, nil
}

// loadVMs loads VMs from database
func (vm *VMManagerService) loadVMs() error {
	var vms []VMInstance
	if err := vm.db.Find(&vms).Error; err != nil {
		return err
	}

	vm.mu.Lock()
	defer vm.mu.Unlock()

	runningCount := 0
	for _, vmInstance := range vms {
		vm.vms[vmInstance.ID] = &vmInstance
		if vmInstance.Status == "running" {
			runningCount++
		}
	}

	vm.metrics.VMsTotal.Set(float64(len(vm.vms)))
	vm.metrics.VMsRunning.Set(float64(runningCount))
	return nil
}

// CreateVM creates a new VM instance
func (vm *VMManagerService) CreateVM(vmInstance *VMInstance) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if len(vm.vms) >= vm.maxVMs {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("maximum number of VMs (%d) reached", vm.maxVMs)
	}

	vmInstance.CreatedAt = time.Now()
	vmInstance.UpdatedAt = time.Now()
	vmInstance.Status = "stopped"

	if err := vm.db.Create(vmInstance).Error; err != nil {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("failed to create VM: %v", err)
	}

	vm.vms[vmInstance.ID] = vmInstance
	vm.metrics.VMsTotal.Set(float64(len(vm.vms)))

	log.Printf("VM %s created for chain %s", vmInstance.ID, vmInstance.ChainID)
	return nil
}

// StartVM starts a VM instance
func (vm *VMManagerService) StartVM(vmID string) error {
	start := time.Now()
	defer func() {
		vm.metrics.VMStartDuration.Observe(time.Since(start).Seconds())
	}()

	vm.mu.Lock()
	defer vm.mu.Unlock()

	vmInstance, exists := vm.vms[vmID]
	if !exists {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("VM %s not found", vmID)
	}

	if vmInstance.Status == "running" {
		return fmt.Errorf("VM %s is already running", vmID)
	}

	// Simulate VM startup process
	vmInstance.Status = "starting"
	vmInstance.UpdatedAt = time.Now()

	if err := vm.db.Save(vmInstance).Error; err != nil {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("failed to update VM status: %v", err)
	}

	// Simulate startup time
	time.Sleep(100 * time.Millisecond)

	now := time.Now()
	vmInstance.Status = "running"
	vmInstance.StartedAt = &now
	vmInstance.UpdatedAt = now

	if err := vm.db.Save(vmInstance).Error; err != nil {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("failed to update VM status: %v", err)
	}

	vm.metrics.VMsStarted.Inc()
	vm.updateRunningCount()

	// Publish VM started event
	eventData, _ := json.Marshal(map[string]interface{}{
		"event":  "vm_started",
		"vm_id":  vmID,
		"chain_id": vmInstance.ChainID,
		"timestamp": time.Now(),
	})
	vm.redis.Publish(context.Background(), "vm_events", eventData)

	log.Printf("VM %s started successfully", vmID)
	return nil
}

// StopVM stops a VM instance
func (vm *VMManagerService) StopVM(vmID string) error {
	start := time.Now()
	defer func() {
		vm.metrics.VMStopDuration.Observe(time.Since(start).Seconds())
	}()

	vm.mu.Lock()
	defer vm.mu.Unlock()

	vmInstance, exists := vm.vms[vmID]
	if !exists {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("VM %s not found", vmID)
	}

	if vmInstance.Status == "stopped" {
		return fmt.Errorf("VM %s is already stopped", vmID)
	}

	// Simulate VM shutdown process
	vmInstance.Status = "stopping"
	vmInstance.UpdatedAt = time.Now()

	if err := vm.db.Save(vmInstance).Error; err != nil {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("failed to update VM status: %v", err)
	}

	// Simulate shutdown time
	time.Sleep(50 * time.Millisecond)

	now := time.Now()
	vmInstance.Status = "stopped"
	vmInstance.StoppedAt = &now
	vmInstance.UpdatedAt = now

	if err := vm.db.Save(vmInstance).Error; err != nil {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("failed to update VM status: %v", err)
	}

	vm.metrics.VMsStopped.Inc()
	vm.updateRunningCount()

	// Publish VM stopped event
	eventData, _ := json.Marshal(map[string]interface{}{
		"event":  "vm_stopped",
		"vm_id":  vmID,
		"chain_id": vmInstance.ChainID,
		"timestamp": time.Now(),
	})
	vm.redis.Publish(context.Background(), "vm_events", eventData)

	log.Printf("VM %s stopped successfully", vmID)
	return nil
}

// DeleteVM deletes a VM instance
func (vm *VMManagerService) DeleteVM(vmID string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	vmInstance, exists := vm.vms[vmID]
	if !exists {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("VM %s not found", vmID)
	}

	if vmInstance.Status == "running" {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("cannot delete running VM %s", vmID)
	}

	if err := vm.db.Delete(vmInstance).Error; err != nil {
		vm.metrics.VMErrors.Inc()
		return fmt.Errorf("failed to delete VM: %v", err)
	}

	delete(vm.vms, vmID)
	vm.metrics.VMsTotal.Set(float64(len(vm.vms)))

	log.Printf("VM %s deleted", vmID)
	return nil
}

// updateRunningCount updates the running VM count metric
func (vm *VMManagerService) updateRunningCount() {
	runningCount := 0
	for _, vmInstance := range vm.vms {
		if vmInstance.Status == "running" {
			runningCount++
		}
	}
	vm.metrics.VMsRunning.Set(float64(runningCount))
}

// HTTP Handlers

func (vm *VMManagerService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (vm *VMManagerService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check database connection
	sqlDB, err := vm.db.DB()
	if err != nil || sqlDB.Ping() != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "database unavailable"})
		return
	}

	// Check Redis connection
	if err := vm.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (vm *VMManagerService) statusHandler(w http.ResponseWriter, r *http.Request) {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	runningCount := 0
	for _, vmInstance := range vm.vms {
		if vmInstance.Status == "running" {
			runningCount++
		}
	}

	status := map[string]interface{}{
		"total_vms":    len(vm.vms),
		"running_vms":  runningCount,
		"max_vms":      vm.maxVMs,
		"timestamp":    time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (vm *VMManagerService) createVMHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var vmInstance VMInstance
	if err := json.NewDecoder(r.Body).Decode(&vmInstance); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid VM data"})
		return
	}

	if err := vm.CreateVM(&vmInstance); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "VM created", "vm_id": vmInstance.ID})
}

func (vm *VMManagerService) startVMHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	vars := mux.Vars(r)
	vmID := vars["id"]

	if err := vm.StartVM(vmID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "VM started", "vm_id": vmID})
}

func (vm *VMManagerService) stopVMHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	vars := mux.Vars(r)
	vmID := vars["id"]

	if err := vm.StopVM(vmID); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "VM stopped", "vm_id": vmID})
}

func (vm *VMManagerService) getVMsHandler(w http.ResponseWriter, r *http.Request) {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	vms := make([]*VMInstance, 0, len(vm.vms))
	for _, vmInstance := range vm.vms {
		vms = append(vms, vmInstance)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(vms)
}

func (vm *VMManagerService) getVMHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	vmID := vars["id"]

	vm.mu.RLock()
	vmInstance, exists := vm.vms[vmID]
	vm.mu.RUnlock()

	if !exists {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "VM not found"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(vmInstance)
}

// setupRoutes sets up HTTP routes
func (vm *VMManagerService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health checks
	router.HandleFunc("/health", vm.healthHandler).Methods("GET")
	router.HandleFunc("/ready", vm.readyHandler).Methods("GET")
	router.HandleFunc("/startup", vm.healthHandler).Methods("GET")

	// API endpoints
	router.HandleFunc("/status", vm.statusHandler).Methods("GET")
	router.HandleFunc("/vms", vm.createVMHandler).Methods("POST")
	router.HandleFunc("/vms", vm.getVMsHandler).Methods("GET")
	router.HandleFunc("/vms/{id}", vm.getVMHandler).Methods("GET")
	router.HandleFunc("/vms/{id}/start", vm.startVMHandler).Methods("POST")
	router.HandleFunc("/vms/{id}/stop", vm.stopVMHandler).Methods("POST")

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
	log.Println("Starting VM Manager Service...")

	// Create VM manager service
	service, err := NewVMManagerService()
	if err != nil {
		log.Fatalf("Failed to create VM manager service: %v", err)
	}

	// Setup HTTP server
	router := service.setupRoutes()
	server := &http.Server{
		Addr:         ":8081",
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Println("VM Manager Service listening on :8081")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down VM Manager Service...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("VM Manager Service stopped")
} 