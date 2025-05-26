package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

// PeerManagerService manages peer connections and discovery
type PeerManagerService struct {
	peers       map[string]*Peer
	mu          sync.RWMutex
	redis       *redis.Client
	metrics     *PeerMetrics
	maxPeers    int
	nodeID      string
}

// Peer represents a network peer
type Peer struct {
	ID            string    `json:"id"`
	Address       string    `json:"address"`
	Port          int       `json:"port"`
	NodeID        string    `json:"node_id"`
	Connected     bool      `json:"connected"`
	LastSeen      time.Time `json:"last_seen"`
	Version       string    `json:"version"`
	Latency       int64     `json:"latency_ms"`
	Uptime        float64   `json:"uptime"`
	IsValidator   bool      `json:"is_validator"`
	Stake         int64     `json:"stake"`
	SubnetIDs     []string  `json:"subnet_ids"`
	ConnectionAge int64     `json:"connection_age_seconds"`
}

// PeerMetrics holds Prometheus metrics
type PeerMetrics struct {
	TotalPeers        prometheus.Gauge
	ConnectedPeers    prometheus.Gauge
	ValidatorPeers    prometheus.Gauge
	PeerConnections   *prometheus.CounterVec
	PeerLatency       prometheus.Histogram
	PeerUptime        prometheus.Histogram
	ConnectionErrors  prometheus.Counter
}

// NewPeerMetrics creates new metrics
func NewPeerMetrics() *PeerMetrics {
	return &PeerMetrics{
		TotalPeers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "peer_manager_total_peers",
			Help: "Total number of known peers",
		}),
		ConnectedPeers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "peer_manager_connected_peers",
			Help: "Number of connected peers",
		}),
		ValidatorPeers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "peer_manager_validator_peers",
			Help: "Number of validator peers",
		}),
		PeerConnections: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "peer_manager_connections_total",
				Help: "Total number of peer connections",
			},
			[]string{"type", "status"},
		),
		PeerLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "peer_manager_peer_latency_seconds",
			Help:    "Peer latency distribution",
			Buckets: prometheus.DefBuckets,
		}),
		PeerUptime: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "peer_manager_peer_uptime_ratio",
			Help:    "Peer uptime ratio distribution",
			Buckets: []float64{0.5, 0.7, 0.8, 0.9, 0.95, 0.99, 1.0},
		}),
		ConnectionErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "peer_manager_connection_errors_total",
			Help: "Total number of connection errors",
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *PeerMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.TotalPeers)
	prometheus.MustRegister(m.ConnectedPeers)
	prometheus.MustRegister(m.ValidatorPeers)
	prometheus.MustRegister(m.PeerConnections)
	prometheus.MustRegister(m.PeerLatency)
	prometheus.MustRegister(m.PeerUptime)
	prometheus.MustRegister(m.ConnectionErrors)
}

// NewPeerManagerService creates a new peer manager service
func NewPeerManagerService() (*PeerManagerService, error) {
	// Parse configuration
	maxPeersStr := getEnv("MAX_PEERS", "100")
	maxPeers, err := strconv.Atoi(maxPeersStr)
	if err != nil {
		maxPeers = 100
	}

	nodeID := getEnv("NODE_ID", "peer-manager-1")

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
	metrics := NewPeerMetrics()
	metrics.RegisterMetrics()

	service := &PeerManagerService{
		peers:    make(map[string]*Peer),
		redis:    redisClient,
		metrics:  metrics,
		maxPeers: maxPeers,
		nodeID:   nodeID,
	}

	// Start background tasks
	go service.updateMetrics()
	go service.peerMaintenance()
	go service.peerDiscovery()

	return service, nil
}

// AddPeer adds a new peer
func (pms *PeerManagerService) AddPeer(peer *Peer) error {
	pms.mu.Lock()
	defer pms.mu.Unlock()

	// Check if we've reached max peers
	if len(pms.peers) >= pms.maxPeers && !pms.peers[peer.ID].Connected {
		return fmt.Errorf("maximum number of peers reached (%d)", pms.maxPeers)
	}

	// Check if peer already exists
	if existingPeer, exists := pms.peers[peer.ID]; exists {
		// Update existing peer
		existingPeer.Address = peer.Address
		existingPeer.Port = peer.Port
		existingPeer.LastSeen = time.Now()
		existingPeer.Connected = peer.Connected
		if peer.Version != "" {
			existingPeer.Version = peer.Version
		}
		if peer.Latency > 0 {
			existingPeer.Latency = peer.Latency
		}
		if peer.Uptime > 0 {
			existingPeer.Uptime = peer.Uptime
		}
		existingPeer.IsValidator = peer.IsValidator
		existingPeer.Stake = peer.Stake
		existingPeer.SubnetIDs = peer.SubnetIDs
	} else {
		// Add new peer
		peer.LastSeen = time.Now()
		peer.ConnectionAge = 0
		pms.peers[peer.ID] = peer
	}

	// Cache in Redis
	peerData, _ := json.Marshal(peer)
	pms.redis.Set(context.Background(), fmt.Sprintf("peer:%s", peer.ID), peerData, time.Hour)

	pms.metrics.PeerConnections.WithLabelValues("add", "success").Inc()
	log.Printf("Added/Updated peer: %s (%s:%d)", peer.ID, peer.Address, peer.Port)
	return nil
}

// RemovePeer removes a peer
func (pms *PeerManagerService) RemovePeer(peerID string) error {
	pms.mu.Lock()
	defer pms.mu.Unlock()

	if peer, exists := pms.peers[peerID]; exists {
		peer.Connected = false
		peer.LastSeen = time.Now()
		
		// Remove from cache
		pms.redis.Del(context.Background(), fmt.Sprintf("peer:%s", peerID))
		
		pms.metrics.PeerConnections.WithLabelValues("remove", "success").Inc()
		log.Printf("Removed peer: %s", peerID)
		return nil
	}

	return fmt.Errorf("peer %s not found", peerID)
}

// GetPeer retrieves a peer by ID
func (pms *PeerManagerService) GetPeer(peerID string) (*Peer, error) {
	// Try cache first
	cached, err := pms.redis.Get(context.Background(), fmt.Sprintf("peer:%s", peerID)).Result()
	if err == nil {
		var peer Peer
		if json.Unmarshal([]byte(cached), &peer) == nil {
			return &peer, nil
		}
	}

	pms.mu.RLock()
	defer pms.mu.RUnlock()

	if peer, exists := pms.peers[peerID]; exists {
		return peer, nil
	}

	return nil, fmt.Errorf("peer %s not found", peerID)
}

// ListPeers returns all peers with optional filtering
func (pms *PeerManagerService) ListPeers(connectedOnly, validatorsOnly bool) []*Peer {
	pms.mu.RLock()
	defer pms.mu.RUnlock()

	peers := make([]*Peer, 0)
	for _, peer := range pms.peers {
		if connectedOnly && !peer.Connected {
			continue
		}
		if validatorsOnly && !peer.IsValidator {
			continue
		}
		peers = append(peers, peer)
	}

	// Sort by connection age (newest first)
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].ConnectionAge > peers[j].ConnectionAge
	})

	return peers
}

// GetBestPeers returns the best peers based on latency and uptime
func (pms *PeerManagerService) GetBestPeers(count int) []*Peer {
	pms.mu.RLock()
	defer pms.mu.RUnlock()

	connectedPeers := make([]*Peer, 0)
	for _, peer := range pms.peers {
		if peer.Connected {
			connectedPeers = append(connectedPeers, peer)
		}
	}

	// Sort by score (uptime * 100 - latency)
	sort.Slice(connectedPeers, func(i, j int) bool {
		scoreI := connectedPeers[i].Uptime*100 - float64(connectedPeers[i].Latency)
		scoreJ := connectedPeers[j].Uptime*100 - float64(connectedPeers[j].Latency)
		return scoreI > scoreJ
	})

	if count > len(connectedPeers) {
		count = len(connectedPeers)
	}

	return connectedPeers[:count]
}

// UpdatePeerLatency updates peer latency
func (pms *PeerManagerService) UpdatePeerLatency(peerID string, latency int64) error {
	pms.mu.Lock()
	defer pms.mu.Unlock()

	if peer, exists := pms.peers[peerID]; exists {
		peer.Latency = latency
		peer.LastSeen = time.Now()
		
		// Update cache
		peerData, _ := json.Marshal(peer)
		pms.redis.Set(context.Background(), fmt.Sprintf("peer:%s", peerID), peerData, time.Hour)
		
		return nil
	}

	return fmt.Errorf("peer %s not found", peerID)
}

// UpdatePeerUptime updates peer uptime
func (pms *PeerManagerService) UpdatePeerUptime(peerID string, uptime float64) error {
	pms.mu.Lock()
	defer pms.mu.Unlock()

	if peer, exists := pms.peers[peerID]; exists {
		peer.Uptime = uptime
		peer.LastSeen = time.Now()
		
		// Update cache
		peerData, _ := json.Marshal(peer)
		pms.redis.Set(context.Background(), fmt.Sprintf("peer:%s", peerID), peerData, time.Hour)
		
		return nil
	}

	return fmt.Errorf("peer %s not found", peerID)
}

// updateMetrics updates Prometheus metrics
func (pms *PeerManagerService) updateMetrics() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		pms.mu.RLock()
		totalPeers := len(pms.peers)
		connectedPeers := 0
		validatorPeers := 0

		for _, peer := range pms.peers {
			if peer.Connected {
				connectedPeers++
				pms.metrics.PeerLatency.Observe(float64(peer.Latency) / 1000.0) // Convert to seconds
				pms.metrics.PeerUptime.Observe(peer.Uptime / 100.0)             // Convert to ratio
			}
			if peer.IsValidator {
				validatorPeers++
			}
		}
		pms.mu.RUnlock()

		pms.metrics.TotalPeers.Set(float64(totalPeers))
		pms.metrics.ConnectedPeers.Set(float64(connectedPeers))
		pms.metrics.ValidatorPeers.Set(float64(validatorPeers))
	}
}

// peerMaintenance performs peer maintenance tasks
func (pms *PeerManagerService) peerMaintenance() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		pms.mu.Lock()
		now := time.Now()
		
		for peerID, peer := range pms.peers {
			// Update connection age
			if peer.Connected {
				peer.ConnectionAge = int64(now.Sub(peer.LastSeen).Seconds())
			}

			// Disconnect peers that haven't been seen for 5 minutes
			if now.Sub(peer.LastSeen) > 5*time.Minute {
				if peer.Connected {
					log.Printf("Peer %s timed out, marking as disconnected", peerID)
					peer.Connected = false
					pms.metrics.ConnectionErrors.Inc()
				}
			}

			// Remove peers that haven't been seen for 30 minutes
			if now.Sub(peer.LastSeen) > 30*time.Minute {
				delete(pms.peers, peerID)
				pms.redis.Del(context.Background(), fmt.Sprintf("peer:%s", peerID))
				log.Printf("Removed stale peer: %s", peerID)
			}
		}
		pms.mu.Unlock()
	}
}

// peerDiscovery performs peer discovery
func (pms *PeerManagerService) peerDiscovery() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		// Publish peer list for discovery
		peers := pms.ListPeers(true, false) // Connected peers only
		if len(peers) > 0 {
			peerList := make([]map[string]interface{}, 0, len(peers))
			for _, peer := range peers {
				peerList = append(peerList, map[string]interface{}{
					"id":      peer.ID,
					"address": peer.Address,
					"port":    peer.Port,
					"node_id": peer.NodeID,
				})
			}

			discoveryData := map[string]interface{}{
				"source": pms.nodeID,
				"peers":  peerList,
				"timestamp": time.Now(),
			}

			data, _ := json.Marshal(discoveryData)
			pms.redis.Publish(context.Background(), "peer_discovery", data)
		}
	}
}

// HTTP Handlers

func (pms *PeerManagerService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (pms *PeerManagerService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check Redis connection
	if err := pms.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (pms *PeerManagerService) statusHandler(w http.ResponseWriter, r *http.Request) {
	pms.mu.RLock()
	totalPeers := len(pms.peers)
	connectedPeers := 0
	validatorPeers := 0

	for _, peer := range pms.peers {
		if peer.Connected {
			connectedPeers++
		}
		if peer.IsValidator {
			validatorPeers++
		}
	}
	pms.mu.RUnlock()

	status := map[string]interface{}{
		"service":         "peer-manager",
		"node_id":         pms.nodeID,
		"total_peers":     totalPeers,
		"connected_peers": connectedPeers,
		"validator_peers": validatorPeers,
		"max_peers":       pms.maxPeers,
		"timestamp":       time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (pms *PeerManagerService) addPeerHandler(w http.ResponseWriter, r *http.Request) {
	var peer Peer
	if err := json.NewDecoder(r.Body).Decode(&peer); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := pms.AddPeer(&peer); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(peer)
}

func (pms *PeerManagerService) getPeerHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	peerID := vars["id"]

	peer, err := pms.GetPeer(peerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peer)
}

func (pms *PeerManagerService) listPeersHandler(w http.ResponseWriter, r *http.Request) {
	connectedOnly := r.URL.Query().Get("connected") == "true"
	validatorsOnly := r.URL.Query().Get("validators") == "true"

	peers := pms.ListPeers(connectedOnly, validatorsOnly)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peers)
}

func (pms *PeerManagerService) getBestPeersHandler(w http.ResponseWriter, r *http.Request) {
	countStr := r.URL.Query().Get("count")
	count := 10 // default
	if countStr != "" {
		if c, err := strconv.Atoi(countStr); err == nil {
			count = c
		}
	}

	peers := pms.GetBestPeers(count)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peers)
}

func (pms *PeerManagerService) removePeerHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	peerID := vars["id"]

	if err := pms.RemovePeer(peerID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
}

// setupRoutes configures HTTP routes
func (pms *PeerManagerService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", pms.healthHandler).Methods("GET")
	router.HandleFunc("/ready", pms.readyHandler).Methods("GET")
	router.HandleFunc("/status", pms.statusHandler).Methods("GET")

	// Peer management endpoints
	router.HandleFunc("/peers", pms.addPeerHandler).Methods("POST")
	router.HandleFunc("/peers", pms.listPeersHandler).Methods("GET")
	router.HandleFunc("/peers/best", pms.getBestPeersHandler).Methods("GET")
	router.HandleFunc("/peers/{id}", pms.getPeerHandler).Methods("GET")
	router.HandleFunc("/peers/{id}", pms.removePeerHandler).Methods("DELETE")

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
	service, err := NewPeerManagerService()
	if err != nil {
		log.Fatalf("Failed to create peer manager service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8086")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Peer Manager Service starting on port %s", port)
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