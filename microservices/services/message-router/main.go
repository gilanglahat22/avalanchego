package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

// MessageRouterService handles inter-service message routing
type MessageRouterService struct {
	redis       *redis.Client
	subscribers map[string][]chan *Message
	mu          sync.RWMutex
	metrics     *RouterMetrics
	upgrader    websocket.Upgrader
}

// Message represents a routed message
type Message struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Source    string                 `json:"source"`
	Target    string                 `json:"target"`
	Topic     string                 `json:"topic"`
	Data      map[string]interface{} `json:"data"`
	Timestamp time.Time              `json:"timestamp"`
	Priority  int                    `json:"priority"`
}

// RouterMetrics holds Prometheus metrics
type RouterMetrics struct {
	MessagesRouted    *prometheus.CounterVec
	MessagesQueued    prometheus.Gauge
	RoutingLatency    prometheus.Histogram
	ActiveConnections prometheus.Gauge
	RoutingErrors     prometheus.Counter
}

// NewRouterMetrics creates new metrics
func NewRouterMetrics() *RouterMetrics {
	return &RouterMetrics{
		MessagesRouted: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "message_router_messages_routed_total",
				Help: "Total number of messages routed",
			},
			[]string{"source", "target", "type"},
		),
		MessagesQueued: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "message_router_messages_queued",
			Help: "Number of messages currently queued",
		}),
		RoutingLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "message_router_routing_duration_seconds",
			Help:    "Time taken to route messages",
			Buckets: prometheus.DefBuckets,
		}),
		ActiveConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "message_router_active_connections",
			Help: "Number of active WebSocket connections",
		}),
		RoutingErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "message_router_errors_total",
			Help: "Total number of routing errors",
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *RouterMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.MessagesRouted)
	prometheus.MustRegister(m.MessagesQueued)
	prometheus.MustRegister(m.RoutingLatency)
	prometheus.MustRegister(m.ActiveConnections)
	prometheus.MustRegister(m.RoutingErrors)
}

// NewMessageRouterService creates a new message router service
func NewMessageRouterService() (*MessageRouterService, error) {
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
	metrics := NewRouterMetrics()
	metrics.RegisterMetrics()

	service := &MessageRouterService{
		redis:       redisClient,
		subscribers: make(map[string][]chan *Message),
		metrics:     metrics,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins in development
			},
		},
	}

	// Start message processing
	go service.processMessages()

	return service, nil
}

// RouteMessage routes a message to its destination
func (mrs *MessageRouterService) RouteMessage(message *Message) error {
	start := time.Now()
	defer func() {
		mrs.metrics.RoutingLatency.Observe(time.Since(start).Seconds())
	}()

	// Validate message
	if message.Type == "" || message.Target == "" {
		mrs.metrics.RoutingErrors.Inc()
		return fmt.Errorf("invalid message: missing type or target")
	}

	// Set timestamp if not provided
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	}

	// Route based on target
	switch message.Target {
	case "consensus":
		return mrs.routeToConsensus(message)
	case "vm-manager":
		return mrs.routeToVMManager(message)
	case "chain-manager":
		return mrs.routeToChainManager(message)
	case "validator":
		return mrs.routeToValidator(message)
	case "p2p-network":
		return mrs.routeToP2PNetwork(message)
	case "broadcast":
		return mrs.broadcastMessage(message)
	default:
		return mrs.routeToTopic(message)
	}
}

// routeToConsensus routes messages to consensus service
func (mrs *MessageRouterService) routeToConsensus(message *Message) error {
	topic := "consensus_messages"
	return mrs.publishToRedis(topic, message)
}

// routeToVMManager routes messages to VM manager service
func (mrs *MessageRouterService) routeToVMManager(message *Message) error {
	topic := "vm_manager_messages"
	return mrs.publishToRedis(topic, message)
}

// routeToChainManager routes messages to chain manager service
func (mrs *MessageRouterService) routeToChainManager(message *Message) error {
	topic := "chain_manager_messages"
	return mrs.publishToRedis(topic, message)
}

// routeToValidator routes messages to validator service
func (mrs *MessageRouterService) routeToValidator(message *Message) error {
	topic := "validator_messages"
	return mrs.publishToRedis(topic, message)
}

// routeToP2PNetwork routes messages to P2P network service
func (mrs *MessageRouterService) routeToP2PNetwork(message *Message) error {
	topic := "p2p_messages"
	return mrs.publishToRedis(topic, message)
}

// broadcastMessage broadcasts message to all services
func (mrs *MessageRouterService) broadcastMessage(message *Message) error {
	topics := []string{
		"consensus_messages",
		"vm_manager_messages",
		"chain_manager_messages",
		"validator_messages",
		"p2p_messages",
	}

	for _, topic := range topics {
		if err := mrs.publishToRedis(topic, message); err != nil {
			log.Printf("Failed to broadcast to topic %s: %v", topic, err)
		}
	}

	return nil
}

// routeToTopic routes message to a specific topic
func (mrs *MessageRouterService) routeToTopic(message *Message) error {
	topic := message.Topic
	if topic == "" {
		topic = message.Target
	}
	return mrs.publishToRedis(topic, message)
}

// publishToRedis publishes message to Redis
func (mrs *MessageRouterService) publishToRedis(topic string, message *Message) error {
	messageData, err := json.Marshal(message)
	if err != nil {
		mrs.metrics.RoutingErrors.Inc()
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	if err := mrs.redis.Publish(context.Background(), topic, messageData).Err(); err != nil {
		mrs.metrics.RoutingErrors.Inc()
		return fmt.Errorf("failed to publish to Redis: %v", err)
	}

	mrs.metrics.MessagesRouted.WithLabelValues(message.Source, message.Target, message.Type).Inc()
	log.Printf("Routed message from %s to %s (type: %s)", message.Source, message.Target, message.Type)
	return nil
}

// processMessages processes incoming messages from Redis
func (mrs *MessageRouterService) processMessages() {
	pubsub := mrs.redis.Subscribe(context.Background(), "router_messages")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for msg := range ch {
		var message Message
		if err := json.Unmarshal([]byte(msg.Payload), &message); err != nil {
			log.Printf("Failed to unmarshal message: %v", err)
			continue
		}

		if err := mrs.RouteMessage(&message); err != nil {
			log.Printf("Failed to route message: %v", err)
		}
	}
}

// Subscribe allows services to subscribe to message topics
func (mrs *MessageRouterService) Subscribe(topic string) chan *Message {
	mrs.mu.Lock()
	defer mrs.mu.Unlock()

	ch := make(chan *Message, 100)
	mrs.subscribers[topic] = append(mrs.subscribers[topic], ch)
	return ch
}

// HTTP Handlers

func (mrs *MessageRouterService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (mrs *MessageRouterService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check Redis connection
	if err := mrs.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (mrs *MessageRouterService) statusHandler(w http.ResponseWriter, r *http.Request) {
	mrs.mu.RLock()
	subscriberCount := len(mrs.subscribers)
	mrs.mu.RUnlock()

	status := map[string]interface{}{
		"service":     "message-router",
		"subscribers": subscriberCount,
		"timestamp":   time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (mrs *MessageRouterService) routeMessageHandler(w http.ResponseWriter, r *http.Request) {
	var message Message
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if err := mrs.RouteMessage(&message); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "routed"})
}

func (mrs *MessageRouterService) websocketHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := mrs.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	mrs.metrics.ActiveConnections.Inc()
	defer mrs.metrics.ActiveConnections.Dec()

	// Handle WebSocket messages
	for {
		var message Message
		if err := conn.ReadJSON(&message); err != nil {
			log.Printf("WebSocket read error: %v", err)
			break
		}

		if err := mrs.RouteMessage(&message); err != nil {
			log.Printf("Failed to route WebSocket message: %v", err)
		}
	}
}

// setupRoutes configures HTTP routes
func (mrs *MessageRouterService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", mrs.healthHandler).Methods("GET")
	router.HandleFunc("/ready", mrs.readyHandler).Methods("GET")
	router.HandleFunc("/status", mrs.statusHandler).Methods("GET")

	// Message routing endpoints
	router.HandleFunc("/route", mrs.routeMessageHandler).Methods("POST")
	router.HandleFunc("/ws", mrs.websocketHandler)

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
	service, err := NewMessageRouterService()
	if err != nil {
		log.Fatalf("Failed to create message router service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8085")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Message Router Service starting on port %s", port)
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