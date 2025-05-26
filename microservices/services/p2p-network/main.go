package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

// P2PNetworkService represents the main P2P network service
type P2PNetworkService struct {
	nodeID        string
	tcpPort       int
	udpPort       int
	peers         map[string]*Peer
	bootstrapNodes []string
	redis         *redis.Client
	mu            sync.RWMutex
	metrics       *P2PMetrics
	tcpListener   net.Listener
	udpConn       *net.UDPConn
}

// Peer represents a network peer
type Peer struct {
	ID        string    `json:"id"`
	Address   string    `json:"address"`
	Port      int       `json:"port"`
	Connected bool      `json:"connected"`
	LastSeen  time.Time `json:"last_seen"`
	Version   string    `json:"version"`
	Latency   int64     `json:"latency_ms"`
}

// Message represents a P2P message
type Message struct {
	Type      string      `json:"type"`
	From      string      `json:"from"`
	To        string      `json:"to"`
	Data      interface{} `json:"data"`
	Timestamp time.Time   `json:"timestamp"`
}

// P2PMetrics holds Prometheus metrics
type P2PMetrics struct {
	PeerCount         prometheus.Gauge
	ConnectedPeers    prometheus.Gauge
	MessagesReceived  prometheus.Counter
	MessagesSent      prometheus.Counter
	ConnectionErrors  prometheus.Counter
	NetworkLatency    prometheus.Histogram
}

// NewP2PMetrics creates new metrics
func NewP2PMetrics() *P2PMetrics {
	return &P2PMetrics{
		PeerCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "p2p_peer_count",
			Help: "Total number of known peers",
		}),
		ConnectedPeers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "p2p_connected_peers",
			Help: "Number of connected peers",
		}),
		MessagesReceived: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "p2p_messages_received_total",
			Help: "Total number of messages received",
		}),
		MessagesSent: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "p2p_messages_sent_total",
			Help: "Total number of messages sent",
		}),
		ConnectionErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "p2p_connection_errors_total",
			Help: "Total number of connection errors",
		}),
		NetworkLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "p2p_network_latency_seconds",
			Help:    "Network latency to peers",
			Buckets: prometheus.DefBuckets,
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *P2PMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.PeerCount)
	prometheus.MustRegister(m.ConnectedPeers)
	prometheus.MustRegister(m.MessagesReceived)
	prometheus.MustRegister(m.MessagesSent)
	prometheus.MustRegister(m.ConnectionErrors)
	prometheus.MustRegister(m.NetworkLatency)
}

// NewP2PNetworkService creates a new P2P network service
func NewP2PNetworkService() (*P2PNetworkService, error) {
	// Parse configuration
	nodeID := getEnv("NODE_ID", "node-1")
	tcpPortStr := getEnv("TCP_PORT", "9651")
	udpPortStr := getEnv("UDP_PORT", "9651")
	bootstrapNodesStr := getEnv("BOOTSTRAP_NODES", "")

	tcpPort, err := strconv.Atoi(tcpPortStr)
	if err != nil {
		tcpPort = 9651
	}

	udpPort, err := strconv.Atoi(udpPortStr)
	if err != nil {
		udpPort = 9651
	}

	var bootstrapNodes []string
	if bootstrapNodesStr != "" {
		bootstrapNodes = strings.Split(bootstrapNodesStr, ",")
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
	metrics := NewP2PMetrics()
	metrics.RegisterMetrics()

	service := &P2PNetworkService{
		nodeID:         nodeID,
		tcpPort:        tcpPort,
		udpPort:        udpPort,
		peers:          make(map[string]*Peer),
		bootstrapNodes: bootstrapNodes,
		redis:          redisClient,
		metrics:        metrics,
	}

	return service, nil
}

// Start starts the P2P network service
func (p2p *P2PNetworkService) Start() error {
	// Start TCP listener
	if err := p2p.startTCPListener(); err != nil {
		return fmt.Errorf("failed to start TCP listener: %v", err)
	}

	// Start UDP listener
	if err := p2p.startUDPListener(); err != nil {
		return fmt.Errorf("failed to start UDP listener: %v", err)
	}

	// Connect to bootstrap nodes
	go p2p.connectToBootstrapNodes()

	// Start peer discovery
	go p2p.startPeerDiscovery()

	// Start peer maintenance
	go p2p.startPeerMaintenance()

	log.Printf("P2P Network Service started on TCP:%d, UDP:%d", p2p.tcpPort, p2p.udpPort)
	return nil
}

// startTCPListener starts the TCP listener
func (p2p *P2PNetworkService) startTCPListener() error {
	addr := fmt.Sprintf(":%d", p2p.tcpPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	p2p.tcpListener = listener

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				p2p.metrics.ConnectionErrors.Inc()
				log.Printf("TCP accept error: %v", err)
				continue
			}

			go p2p.handleTCPConnection(conn)
		}
	}()

	return nil
}

// startUDPListener starts the UDP listener
func (p2p *P2PNetworkService) startUDPListener() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", p2p.udpPort))
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}

	p2p.udpConn = conn

	go func() {
		buffer := make([]byte, 4096)
		for {
			n, clientAddr, err := conn.ReadFromUDP(buffer)
			if err != nil {
				p2p.metrics.ConnectionErrors.Inc()
				log.Printf("UDP read error: %v", err)
				continue
			}

			go p2p.handleUDPMessage(buffer[:n], clientAddr)
		}
	}()

	return nil
}

// handleTCPConnection handles incoming TCP connections
func (p2p *P2PNetworkService) handleTCPConnection(conn net.Conn) {
	defer conn.Close()

	// Simple handshake protocol
	handshake := map[string]interface{}{
		"node_id": p2p.nodeID,
		"version": "1.0.0",
		"timestamp": time.Now(),
	}

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(handshake); err != nil {
		p2p.metrics.ConnectionErrors.Inc()
		log.Printf("Failed to send handshake: %v", err)
		return
	}

	// Read peer handshake
	decoder := json.NewDecoder(conn)
	var peerHandshake map[string]interface{}
	if err := decoder.Decode(&peerHandshake); err != nil {
		p2p.metrics.ConnectionErrors.Inc()
		log.Printf("Failed to read peer handshake: %v", err)
		return
	}

	peerID, ok := peerHandshake["node_id"].(string)
	if !ok {
		p2p.metrics.ConnectionErrors.Inc()
		log.Printf("Invalid peer handshake")
		return
	}

	// Add peer
	peer := &Peer{
		ID:        peerID,
		Address:   conn.RemoteAddr().(*net.TCPAddr).IP.String(),
		Port:      conn.RemoteAddr().(*net.TCPAddr).Port,
		Connected: true,
		LastSeen:  time.Now(),
		Version:   "1.0.0",
	}

	p2p.addPeer(peer)

	// Handle messages
	for {
		var message Message
		if err := decoder.Decode(&message); err != nil {
			break
		}

		p2p.handleMessage(&message)
	}

	// Remove peer on disconnect
	p2p.removePeer(peerID)
}

// handleUDPMessage handles incoming UDP messages
func (p2p *P2PNetworkService) handleUDPMessage(data []byte, addr *net.UDPAddr) {
	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		log.Printf("Failed to unmarshal UDP message: %v", err)
		return
	}

	p2p.handleMessage(&message)
}

// handleMessage handles incoming messages
func (p2p *P2PNetworkService) handleMessage(message *Message) {
	p2p.metrics.MessagesReceived.Inc()

	switch message.Type {
	case "ping":
		p2p.handlePing(message)
	case "pong":
		p2p.handlePong(message)
	case "peer_discovery":
		p2p.handlePeerDiscovery(message)
	case "block":
		p2p.handleBlock(message)
	case "transaction":
		p2p.handleTransaction(message)
	default:
		log.Printf("Unknown message type: %s", message.Type)
	}

	// Publish message to Redis for other services
	messageData, _ := json.Marshal(message)
	p2p.redis.Publish(context.Background(), "p2p_messages", messageData)
}

// handlePing handles ping messages
func (p2p *P2PNetworkService) handlePing(message *Message) {
	pong := &Message{
		Type:      "pong",
		From:      p2p.nodeID,
		To:        message.From,
		Data:      map[string]interface{}{"timestamp": time.Now()},
		Timestamp: time.Now(),
	}

	p2p.sendMessage(pong, message.From)
}

// handlePong handles pong messages
func (p2p *P2PNetworkService) handlePong(message *Message) {
	if data, ok := message.Data.(map[string]interface{}); ok {
		if timestampStr, ok := data["timestamp"].(string); ok {
			if timestamp, err := time.Parse(time.RFC3339, timestampStr); err == nil {
				latency := time.Since(timestamp)
				p2p.metrics.NetworkLatency.Observe(latency.Seconds())

				// Update peer latency
				p2p.mu.Lock()
				if peer, exists := p2p.peers[message.From]; exists {
					peer.Latency = latency.Milliseconds()
					peer.LastSeen = time.Now()
				}
				p2p.mu.Unlock()
			}
		}
	}
}

// handlePeerDiscovery handles peer discovery messages
func (p2p *P2PNetworkService) handlePeerDiscovery(message *Message) {
	if data, ok := message.Data.(map[string]interface{}); ok {
		if peersData, ok := data["peers"].([]interface{}); ok {
			for _, peerData := range peersData {
				if peerMap, ok := peerData.(map[string]interface{}); ok {
					peer := &Peer{
						ID:       peerMap["id"].(string),
						Address:  peerMap["address"].(string),
						Port:     int(peerMap["port"].(float64)),
						LastSeen: time.Now(),
					}
					p2p.addPeer(peer)
				}
			}
		}
	}
}

// handleBlock handles block messages
func (p2p *P2PNetworkService) handleBlock(message *Message) {
	// Forward to consensus service via Redis
	blockData, _ := json.Marshal(message.Data)
	p2p.redis.Publish(context.Background(), "new_block", blockData)
}

// handleTransaction handles transaction messages
func (p2p *P2PNetworkService) handleTransaction(message *Message) {
	// Forward to transaction pool via Redis
	txData, _ := json.Marshal(message.Data)
	p2p.redis.Publish(context.Background(), "new_transaction", txData)
}

// sendMessage sends a message to a specific peer
func (p2p *P2PNetworkService) sendMessage(message *Message, peerID string) error {
	p2p.mu.RLock()
	peer, exists := p2p.peers[peerID]
	p2p.mu.RUnlock()

	if !exists {
		return fmt.Errorf("peer %s not found", peerID)
	}

	// Send via TCP
	addr := fmt.Sprintf("%s:%d", peer.Address, peer.Port)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		p2p.metrics.ConnectionErrors.Inc()
		return err
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(message); err != nil {
		p2p.metrics.ConnectionErrors.Inc()
		return err
	}

	p2p.metrics.MessagesSent.Inc()
	return nil
}

// broadcastMessage broadcasts a message to all connected peers
func (p2p *P2PNetworkService) broadcastMessage(message *Message) {
	p2p.mu.RLock()
	peers := make([]*Peer, 0, len(p2p.peers))
	for _, peer := range p2p.peers {
		if peer.Connected {
			peers = append(peers, peer)
		}
	}
	p2p.mu.RUnlock()

	for _, peer := range peers {
		go p2p.sendMessage(message, peer.ID)
	}
}

// addPeer adds a new peer
func (p2p *P2PNetworkService) addPeer(peer *Peer) {
	p2p.mu.Lock()
	defer p2p.mu.Unlock()

	p2p.peers[peer.ID] = peer
	p2p.updateMetrics()

	log.Printf("Added peer: %s (%s:%d)", peer.ID, peer.Address, peer.Port)
}

// removePeer removes a peer
func (p2p *P2PNetworkService) removePeer(peerID string) {
	p2p.mu.Lock()
	defer p2p.mu.Unlock()

	if peer, exists := p2p.peers[peerID]; exists {
		peer.Connected = false
		p2p.updateMetrics()
		log.Printf("Removed peer: %s", peerID)
	}
}

// updateMetrics updates Prometheus metrics
func (p2p *P2PNetworkService) updateMetrics() {
	connectedCount := 0
	for _, peer := range p2p.peers {
		if peer.Connected {
			connectedCount++
		}
	}

	p2p.metrics.PeerCount.Set(float64(len(p2p.peers)))
	p2p.metrics.ConnectedPeers.Set(float64(connectedCount))
}

// connectToBootstrapNodes connects to bootstrap nodes
func (p2p *P2PNetworkService) connectToBootstrapNodes() {
	for _, node := range p2p.bootstrapNodes {
		parts := strings.Split(node, ":")
		if len(parts) != 2 {
			continue
		}

		address := parts[0]
		port, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}

		peer := &Peer{
			ID:      fmt.Sprintf("bootstrap-%s", address),
			Address: address,
			Port:    port,
		}

		p2p.addPeer(peer)

		// Try to connect
		go func(peer *Peer) {
			addr := fmt.Sprintf("%s:%d", peer.Address, peer.Port)
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				p2p.metrics.ConnectionErrors.Inc()
				log.Printf("Failed to connect to bootstrap node %s: %v", addr, err)
				return
			}
			defer conn.Close()

			peer.Connected = true
			peer.LastSeen = time.Now()
			p2p.updateMetrics()

			log.Printf("Connected to bootstrap node: %s", addr)
		}(peer)
	}
}

// startPeerDiscovery starts peer discovery process
func (p2p *P2PNetworkService) startPeerDiscovery() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Send peer discovery message
		p2p.mu.RLock()
		peers := make([]map[string]interface{}, 0, len(p2p.peers))
		for _, peer := range p2p.peers {
			peers = append(peers, map[string]interface{}{
				"id":      peer.ID,
				"address": peer.Address,
				"port":    peer.Port,
			})
		}
		p2p.mu.RUnlock()

		message := &Message{
			Type: "peer_discovery",
			From: p2p.nodeID,
			Data: map[string]interface{}{
				"peers": peers,
			},
			Timestamp: time.Now(),
		}

		p2p.broadcastMessage(message)
	}
}

// startPeerMaintenance starts peer maintenance process
func (p2p *P2PNetworkService) startPeerMaintenance() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		p2p.mu.Lock()
		now := time.Now()
		for peerID, peer := range p2p.peers {
			// Remove peers that haven't been seen for 5 minutes
			if now.Sub(peer.LastSeen) > 5*time.Minute {
				peer.Connected = false
				log.Printf("Peer %s timed out", peerID)
			}
		}
		p2p.updateMetrics()
		p2p.mu.Unlock()

		// Send ping to connected peers
		ping := &Message{
			Type:      "ping",
			From:      p2p.nodeID,
			Data:      map[string]interface{}{"timestamp": time.Now()},
			Timestamp: time.Now(),
		}

		p2p.broadcastMessage(ping)
	}
}

// HTTP Handlers

func (p2p *P2PNetworkService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (p2p *P2PNetworkService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check Redis connection
	if err := p2p.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (p2p *P2PNetworkService) statusHandler(w http.ResponseWriter, r *http.Request) {
	p2p.mu.RLock()
	defer p2p.mu.RUnlock()

	connectedCount := 0
	for _, peer := range p2p.peers {
		if peer.Connected {
			connectedCount++
		}
	}

	status := map[string]interface{}{
		"node_id":         p2p.nodeID,
		"tcp_port":        p2p.tcpPort,
		"udp_port":        p2p.udpPort,
		"total_peers":     len(p2p.peers),
		"connected_peers": connectedCount,
		"timestamp":       time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (p2p *P2PNetworkService) getPeersHandler(w http.ResponseWriter, r *http.Request) {
	p2p.mu.RLock()
	defer p2p.mu.RUnlock()

	peers := make([]*Peer, 0, len(p2p.peers))
	for _, peer := range p2p.peers {
		peers = append(peers, peer)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peers)
}

// setupRoutes sets up HTTP routes
func (p2p *P2PNetworkService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health checks
	router.HandleFunc("/health", p2p.healthHandler).Methods("GET")
	router.HandleFunc("/ready", p2p.readyHandler).Methods("GET")
	router.HandleFunc("/startup", p2p.healthHandler).Methods("GET")

	// API endpoints
	router.HandleFunc("/status", p2p.statusHandler).Methods("GET")
	router.HandleFunc("/peers", p2p.getPeersHandler).Methods("GET")

	// Metrics
	router.Handle("/metrics", promhttp.Handler())

	return router
}

// Stop stops the P2P network service
func (p2p *P2PNetworkService) Stop() error {
	if p2p.tcpListener != nil {
		p2p.tcpListener.Close()
	}

	if p2p.udpConn != nil {
		p2p.udpConn.Close()
	}

	return nil
}

// getEnv gets environment variable with default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	log.Println("Starting P2P Network Service...")

	// Create P2P network service
	service, err := NewP2PNetworkService()
	if err != nil {
		log.Fatalf("Failed to create P2P network service: %v", err)
	}

	// Start P2P networking
	if err := service.Start(); err != nil {
		log.Fatalf("Failed to start P2P networking: %v", err)
	}

	// Setup HTTP server
	router := service.setupRoutes()
	server := &http.Server{
		Addr:         ":8084",
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Println("P2P Network Service HTTP API listening on :8084")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down P2P Network Service...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("HTTP server forced to shutdown: %v", err)
	}

	if err := service.Stop(); err != nil {
		log.Printf("Failed to stop P2P service: %v", err)
	}

	log.Println("P2P Network Service stopped")
} 