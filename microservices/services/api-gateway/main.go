package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// APIGateway represents the main API gateway service
type APIGateway struct {
	redis       *redis.Client
	jwtSecret   []byte
	routes      map[string]*Route
	rateLimiter *rate.Limiter
	metrics     *GatewayMetrics
	mu          sync.RWMutex
}

// Route represents a gateway route configuration
type Route struct {
	Path     string   `json:"path"`
	Target   string   `json:"target"`
	Methods  []string `json:"methods"`
	AuthRequired bool `json:"auth"`
	Proxy    *httputil.ReverseProxy
}

// GatewayMetrics holds Prometheus metrics
type GatewayMetrics struct {
	RequestsTotal     *prometheus.CounterVec
	RequestDuration   *prometheus.HistogramVec
	RequestsInFlight  prometheus.Gauge
	AuthFailures      prometheus.Counter
	RateLimitHits     prometheus.Counter
}

// NewGatewayMetrics creates new metrics
func NewGatewayMetrics() *GatewayMetrics {
	return &GatewayMetrics{
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		RequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Number of HTTP requests currently being processed",
		}),
		AuthFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "auth_failures_total",
			Help: "Total number of authentication failures",
		}),
		RateLimitHits: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rate_limit_hits_total",
			Help: "Total number of rate limit hits",
		}),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *GatewayMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.RequestsTotal)
	prometheus.MustRegister(m.RequestDuration)
	prometheus.MustRegister(m.RequestsInFlight)
	prometheus.MustRegister(m.AuthFailures)
	prometheus.MustRegister(m.RateLimitHits)
}

// NewAPIGateway creates a new API gateway
func NewAPIGateway() (*APIGateway, error) {
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

	// JWT secret
	jwtSecret := getEnv("JWT_SECRET", "default-secret-key")

	// Rate limiter (100 requests per second with burst of 200)
	rateLimiter := rate.NewLimiter(100, 200)

	// Initialize metrics
	metrics := NewGatewayMetrics()
	metrics.RegisterMetrics()

	gateway := &APIGateway{
		redis:       redisClient,
		jwtSecret:   []byte(jwtSecret),
		routes:      make(map[string]*Route),
		rateLimiter: rateLimiter,
		metrics:     metrics,
	}

	// Initialize routes
	if err := gateway.initializeRoutes(); err != nil {
		return nil, fmt.Errorf("failed to initialize routes: %v", err)
	}

	return gateway, nil
}

// initializeRoutes sets up the gateway routes
func (gw *APIGateway) initializeRoutes() error {
	routes := []*Route{
		{
			Path:     "/api/v1/consensus",
			Target:   getEnv("CONSENSUS_SERVICE_URL", "http://consensus-service:8080"),
			Methods:  []string{"GET", "POST"},
			AuthRequired: true,
		},
		{
			Path:     "/api/v1/vm",
			Target:   getEnv("VM_MANAGER_URL", "http://vm-manager-service:8081"),
			Methods:  []string{"GET", "POST", "PUT", "DELETE"},
			AuthRequired: true,
		},
		{
			Path:     "/api/v1/chain",
			Target:   getEnv("CHAIN_MANAGER_URL", "http://chain-manager-service:8082"),
			Methods:  []string{"GET", "POST"},
			AuthRequired: true,
		},
		{
			Path:     "/api/v1/validator",
			Target:   getEnv("VALIDATOR_SERVICE_URL", "http://validator-service:8083"),
			Methods:  []string{"GET", "POST"},
			AuthRequired: true,
		},
		{
			Path:     "/api/v1/auth",
			Target:   getEnv("AUTH_SERVICE_URL", "http://auth-service:8088"),
			Methods:  []string{"POST"},
			AuthRequired: false,
		},
	}

	for _, route := range routes {
		if err := gw.addRoute(route); err != nil {
			return fmt.Errorf("failed to add route %s: %v", route.Path, err)
		}
	}

	return nil
}

// addRoute adds a new route to the gateway
func (gw *APIGateway) addRoute(route *Route) error {
	targetURL, err := url.Parse(route.Target)
	if err != nil {
		return fmt.Errorf("invalid target URL: %v", err)
	}

	route.Proxy = httputil.NewSingleHostReverseProxy(targetURL)
	
	// Customize the proxy to add headers and handle errors
	route.Proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Set("X-Gateway", "avalanche-api-gateway")
		return nil
	}

	route.Proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Proxy error for %s: %v", r.URL.Path, err)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Service temporarily unavailable",
		})
	}

	gw.mu.Lock()
	gw.routes[route.Path] = route
	gw.mu.Unlock()

	log.Printf("Route added: %s -> %s", route.Path, route.Target)
	return nil
}

// validateJWT validates a JWT token
func (gw *APIGateway) validateJWT(tokenString string) (*jwt.Token, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return gw.jwtSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	return token, nil
}

// authMiddleware handles authentication
func (gw *APIGateway) authMiddleware(next http.Handler, authRequired bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authRequired {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			gw.metrics.AuthFailures.Inc()
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Authorization header required"})
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			gw.metrics.AuthFailures.Inc()
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Bearer token required"})
			return
		}

		token, err := gw.validateJWT(tokenString)
		if err != nil {
			gw.metrics.AuthFailures.Inc()
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid token"})
			return
		}

		// Add user info to request context
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			ctx := context.WithValue(r.Context(), "user", claims)
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware handles rate limiting
func (gw *APIGateway) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gw.rateLimiter.Allow() {
			gw.metrics.RateLimitHits.Inc()
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "Rate limit exceeded"})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// metricsMiddleware handles metrics collection
func (gw *APIGateway) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		gw.metrics.RequestsInFlight.Inc()

		// Create a response writer wrapper to capture status code
		wrapper := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapper, r)

		duration := time.Since(start).Seconds()
		gw.metrics.RequestsInFlight.Dec()
		gw.metrics.RequestsTotal.WithLabelValues(r.Method, r.URL.Path, fmt.Sprintf("%d", wrapper.statusCode)).Inc()
		gw.metrics.RequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// corsMiddleware handles CORS
func (gw *APIGateway) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// proxyHandler handles proxying requests to backend services
func (gw *APIGateway) proxyHandler(w http.ResponseWriter, r *http.Request) {
	gw.mu.RLock()
	defer gw.mu.RUnlock()

	// Find matching route
	var matchedRoute *Route
	for path, route := range gw.routes {
		if strings.HasPrefix(r.URL.Path, path) {
			// Check if method is allowed
			methodAllowed := false
			for _, method := range route.Methods {
				if r.Method == method {
					methodAllowed = true
					break
				}
			}
			if methodAllowed {
				matchedRoute = route
				break
			}
		}
	}

	if matchedRoute == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Route not found"})
		return
	}

	// Add gateway headers
	r.Header.Set("X-Forwarded-By", "avalanche-api-gateway")
	r.Header.Set("X-Request-ID", generateRequestID())

	// Proxy the request
	matchedRoute.Proxy.ServeHTTP(w, r)
}

// generateRequestID generates a unique request ID
func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// Health check handlers
func (gw *APIGateway) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (gw *APIGateway) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check Redis connection
	if err := gw.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (gw *APIGateway) statusHandler(w http.ResponseWriter, r *http.Request) {
	gw.mu.RLock()
	defer gw.mu.RUnlock()

	status := map[string]interface{}{
		"gateway":     "avalanche-api-gateway",
		"version":     "1.0.0",
		"routes":      len(gw.routes),
		"timestamp":   time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// setupRoutes sets up HTTP routes
func (gw *APIGateway) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health checks
	router.HandleFunc("/health", gw.healthHandler).Methods("GET")
	router.HandleFunc("/ready", gw.readyHandler).Methods("GET")
	router.HandleFunc("/startup", gw.healthHandler).Methods("GET")
	router.HandleFunc("/status", gw.statusHandler).Methods("GET")

	// Metrics
	router.Handle("/metrics", promhttp.Handler())

	// API routes with middleware chain
	apiRouter := router.PathPrefix("/api").Subrouter()
	
	// Apply middleware chain
	var handler http.Handler = http.HandlerFunc(gw.proxyHandler)
	
	// Apply middlewares in reverse order (last applied = first executed)
	for path, route := range gw.routes {
		routeHandler := gw.authMiddleware(handler, route.AuthRequired)
		routeHandler = gw.rateLimitMiddleware(routeHandler)
		routeHandler = gw.metricsMiddleware(routeHandler)
		routeHandler = gw.corsMiddleware(routeHandler)
		
		apiRouter.PathPrefix(strings.TrimPrefix(path, "/api")).Handler(routeHandler)
	}

	// Catch-all for API routes
	apiRouter.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Apply middleware chain for unmatched routes
		var handler http.Handler = http.HandlerFunc(gw.proxyHandler)
		handler = gw.rateLimitMiddleware(handler)
		handler = gw.metricsMiddleware(handler)
		handler = gw.corsMiddleware(handler)
		handler.ServeHTTP(w, r)
	})

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
	log.Println("Starting API Gateway...")

	// Create API gateway
	gateway, err := NewAPIGateway()
	if err != nil {
		log.Fatalf("Failed to create API gateway: %v", err)
	}

	// Setup HTTP server
	router := gateway.setupRoutes()
	server := &http.Server{
		Addr:         ":8000",
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Println("API Gateway listening on :8000")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down API Gateway...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	log.Println("API Gateway stopped")
} 