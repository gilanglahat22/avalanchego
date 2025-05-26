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
	"strings"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// AuthService handles authentication and authorization
type AuthService struct {
	redis     *redis.Client
	jwtSecret []byte
	metrics   *AuthMetrics
}

// User represents a user in the system
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Password  string    `json:"-"` // Never expose password in JSON
	Role      string    `json:"role"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LoginRequest represents a login request
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      User      `json:"user"`
}

// RegisterRequest represents a registration request
type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role,omitempty"`
}

// Claims represents JWT claims
type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// AuthMetrics holds Prometheus metrics
type AuthMetrics struct {
	LoginAttempts    *prometheus.CounterVec
	TokensIssued     prometheus.Counter
	TokensValidated  *prometheus.CounterVec
	ActiveSessions   prometheus.Gauge
	AuthErrors       *prometheus.CounterVec
}

// NewAuthMetrics creates new metrics
func NewAuthMetrics() *AuthMetrics {
	return &AuthMetrics{
		LoginAttempts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "auth_login_attempts_total",
				Help: "Total number of login attempts",
			},
			[]string{"status"},
		),
		TokensIssued: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "auth_tokens_issued_total",
			Help: "Total number of tokens issued",
		}),
		TokensValidated: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "auth_tokens_validated_total",
				Help: "Total number of tokens validated",
			},
			[]string{"status"},
		),
		ActiveSessions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "auth_active_sessions",
			Help: "Number of active sessions",
		}),
		AuthErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "auth_errors_total",
				Help: "Total number of authentication errors",
			},
			[]string{"type"},
		),
	}
}

// RegisterMetrics registers metrics with Prometheus
func (m *AuthMetrics) RegisterMetrics() {
	prometheus.MustRegister(m.LoginAttempts)
	prometheus.MustRegister(m.TokensIssued)
	prometheus.MustRegister(m.TokensValidated)
	prometheus.MustRegister(m.ActiveSessions)
	prometheus.MustRegister(m.AuthErrors)
}

// NewAuthService creates a new auth service
func NewAuthService() (*AuthService, error) {
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
	jwtSecret := getEnv("JWT_SECRET", "")
	if jwtSecret == "" {
		// Generate a random secret if not provided
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			return nil, fmt.Errorf("failed to generate JWT secret: %v", err)
		}
		jwtSecret = hex.EncodeToString(secretBytes)
		log.Printf("Generated JWT secret: %s", jwtSecret)
	}

	// Initialize metrics
	metrics := NewAuthMetrics()
	metrics.RegisterMetrics()

	service := &AuthService{
		redis:     redisClient,
		jwtSecret: []byte(jwtSecret),
		metrics:   metrics,
	}

	// Create default admin user if it doesn't exist
	if err := service.createDefaultAdmin(); err != nil {
		log.Printf("Warning: failed to create default admin: %v", err)
	}

	return service, nil
}

// createDefaultAdmin creates a default admin user
func (as *AuthService) createDefaultAdmin() error {
	adminUser := &User{
		ID:        "admin-1",
		Username:  "admin",
		Email:     "admin@avalanche.network",
		Role:      "admin",
		Active:    true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Check if admin already exists
	if _, err := as.GetUser("admin"); err == nil {
		return nil // Admin already exists
	}

	// Hash default password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	adminUser.Password = string(hashedPassword)

	return as.CreateUser(adminUser)
}

// CreateUser creates a new user
func (as *AuthService) CreateUser(user *User) error {
	// Check if user already exists
	if _, err := as.GetUser(user.Username); err == nil {
		return fmt.Errorf("user %s already exists", user.Username)
	}

	// Set timestamps
	user.CreatedAt = time.Now()
	user.UpdatedAt = time.Now()

	// Store user in Redis
	userData, err := json.Marshal(user)
	if err != nil {
		return err
	}

	return as.redis.Set(context.Background(), fmt.Sprintf("user:%s", user.Username), userData, 0).Err()
}

// GetUser retrieves a user by username
func (as *AuthService) GetUser(username string) (*User, error) {
	userData, err := as.redis.Get(context.Background(), fmt.Sprintf("user:%s", username)).Result()
	if err != nil {
		return nil, fmt.Errorf("user not found: %v", err)
	}

	var user User
	if err := json.Unmarshal([]byte(userData), &user); err != nil {
		return nil, err
	}

	return &user, nil
}

// ValidateCredentials validates user credentials
func (as *AuthService) ValidateCredentials(username, password string) (*User, error) {
	user, err := as.GetUser(username)
	if err != nil {
		as.metrics.AuthErrors.WithLabelValues("user_not_found").Inc()
		return nil, err
	}

	if !user.Active {
		as.metrics.AuthErrors.WithLabelValues("user_inactive").Inc()
		return nil, fmt.Errorf("user account is inactive")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		as.metrics.AuthErrors.WithLabelValues("invalid_password").Inc()
		return nil, fmt.Errorf("invalid credentials")
	}

	return user, nil
}

// GenerateToken generates a JWT token for a user
func (as *AuthService) GenerateToken(user *User) (string, time.Time, error) {
	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "avalanche-auth",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(as.jwtSecret)
	if err != nil {
		return "", time.Time{}, err
	}

	// Store token in Redis for session management
	as.redis.Set(context.Background(), fmt.Sprintf("token:%s", tokenString), user.Username, 24*time.Hour)
	as.metrics.TokensIssued.Inc()

	return tokenString, expirationTime, nil
}

// ValidateToken validates a JWT token
func (as *AuthService) ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return as.jwtSecret, nil
	})

	if err != nil {
		as.metrics.TokensValidated.WithLabelValues("invalid").Inc()
		return nil, err
	}

	if !token.Valid {
		as.metrics.TokensValidated.WithLabelValues("invalid").Inc()
		return nil, fmt.Errorf("invalid token")
	}

	// Check if token exists in Redis
	_, err = as.redis.Get(context.Background(), fmt.Sprintf("token:%s", tokenString)).Result()
	if err != nil {
		as.metrics.TokensValidated.WithLabelValues("not_found").Inc()
		return nil, fmt.Errorf("token not found in session store")
	}

	as.metrics.TokensValidated.WithLabelValues("valid").Inc()
	return claims, nil
}

// RevokeToken revokes a JWT token
func (as *AuthService) RevokeToken(tokenString string) error {
	return as.redis.Del(context.Background(), fmt.Sprintf("token:%s", tokenString)).Err()
}

// HTTP Handlers

func (as *AuthService) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func (as *AuthService) readyHandler(w http.ResponseWriter, r *http.Request) {
	// Check Redis connection
	if err := as.redis.Ping(context.Background()).Err(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not ready", "reason": "redis unavailable"})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (as *AuthService) loginHandler(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	user, err := as.ValidateCredentials(req.Username, req.Password)
	if err != nil {
		as.metrics.LoginAttempts.WithLabelValues("failed").Inc()
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	token, expiresAt, err := as.GenerateToken(user)
	if err != nil {
		as.metrics.LoginAttempts.WithLabelValues("error").Inc()
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	as.metrics.LoginAttempts.WithLabelValues("success").Inc()

	response := LoginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		User:      *user,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (as *AuthService) registerHandler(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Validate input
	if req.Username == "" || req.Password == "" || req.Email == "" {
		http.Error(w, "Username, password, and email are required", http.StatusBadRequest)
		return
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	// Set default role if not provided
	role := req.Role
	if role == "" {
		role = "user"
	}

	user := &User{
		ID:       fmt.Sprintf("user-%d", time.Now().Unix()),
		Username: req.Username,
		Email:    req.Email,
		Password: string(hashedPassword),
		Role:     role,
		Active:   true,
	}

	if err := as.CreateUser(user); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	// Remove password from response
	user.Password = ""

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func (as *AuthService) validateHandler(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "Authorization header required", http.StatusUnauthorized)
		return
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	if tokenString == authHeader {
		http.Error(w, "Bearer token required", http.StatusUnauthorized)
		return
	}

	claims, err := as.ValidateToken(tokenString)
	if err != nil {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(claims)
}

func (as *AuthService) logoutHandler(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "Authorization header required", http.StatusUnauthorized)
		return
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")
	if tokenString == authHeader {
		http.Error(w, "Bearer token required", http.StatusUnauthorized)
		return
	}

	if err := as.RevokeToken(tokenString); err != nil {
		http.Error(w, "Failed to revoke token", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "logged out"})
}

// setupRoutes configures HTTP routes
func (as *AuthService) setupRoutes() *mux.Router {
	router := mux.NewRouter()

	// Health endpoints
	router.HandleFunc("/health", as.healthHandler).Methods("GET")
	router.HandleFunc("/ready", as.readyHandler).Methods("GET")

	// Auth endpoints
	router.HandleFunc("/login", as.loginHandler).Methods("POST")
	router.HandleFunc("/register", as.registerHandler).Methods("POST")
	router.HandleFunc("/validate", as.validateHandler).Methods("POST")
	router.HandleFunc("/logout", as.logoutHandler).Methods("POST")

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
	service, err := NewAuthService()
	if err != nil {
		log.Fatalf("Failed to create auth service: %v", err)
	}

	router := service.setupRoutes()
	
	port := getEnv("PORT", "8088")
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Auth Service starting on port %s", port)
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