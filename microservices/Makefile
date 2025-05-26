# AvalancheGo Microservices Makefile
# Provides convenient commands for development, testing, and deployment

# Configuration
DOCKER_REGISTRY ?= avalanche
IMAGE_TAG ?= v1.0.0
ENVIRONMENT ?= development
NAMESPACE_PREFIX = avalanche

# Colors for output
BLUE = \033[0;34m
GREEN = \033[0;32m
YELLOW = \033[1;33m
RED = \033[0;31m
NC = \033[0m # No Color

# Default target
.DEFAULT_GOAL := help

# Help target
.PHONY: help
help: ## Show this help message
	@echo "$(BLUE)AvalancheGo Microservices$(NC)"
	@echo "Available commands:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Development targets
.PHONY: dev-up
dev-up: ## Start development environment with Docker Compose
	@echo "$(BLUE)Starting development environment...$(NC)"
	docker-compose up -d
	@echo "$(GREEN)Development environment started$(NC)"

.PHONY: dev-down
dev-down: ## Stop development environment
	@echo "$(BLUE)Stopping development environment...$(NC)"
	docker-compose down
	@echo "$(GREEN)Development environment stopped$(NC)"

.PHONY: dev-logs
dev-logs: ## Show logs from development environment
	docker-compose logs -f

.PHONY: dev-status
dev-status: ## Show status of development services
	docker-compose ps

.PHONY: dev-clean
dev-clean: ## Clean development environment (remove volumes)
	@echo "$(BLUE)Cleaning development environment...$(NC)"
	docker-compose down -v --remove-orphans
	docker system prune -f
	@echo "$(GREEN)Development environment cleaned$(NC)"

# Build targets
.PHONY: build
build: ## Build all Docker images
	@echo "$(BLUE)Building Docker images...$(NC)"
	./scripts/deploy.sh build
	@echo "$(GREEN)All images built successfully$(NC)"

.PHONY: build-consensus
build-consensus: ## Build consensus service image
	@echo "$(BLUE)Building consensus service...$(NC)"
	cd services/consensus && docker build -t $(DOCKER_REGISTRY)/consensus-service:$(IMAGE_TAG) .

.PHONY: build-gateway
build-gateway: ## Build API gateway image
	@echo "$(BLUE)Building API gateway...$(NC)"
	cd services/api-gateway && docker build -t $(DOCKER_REGISTRY)/api-gateway:$(IMAGE_TAG) .

.PHONY: push
push: ## Push all images to registry
	@echo "$(BLUE)Pushing images to registry...$(NC)"
	docker push $(DOCKER_REGISTRY)/consensus-service:$(IMAGE_TAG)
	docker push $(DOCKER_REGISTRY)/api-gateway:$(IMAGE_TAG)
	@echo "$(GREEN)All images pushed successfully$(NC)"

# Testing targets
.PHONY: test
test: ## Run all tests
	@echo "$(BLUE)Running tests...$(NC)"
	$(MAKE) test-unit
	$(MAKE) test-integration
	@echo "$(GREEN)All tests completed$(NC)"

.PHONY: test-unit
test-unit: ## Run unit tests
	@echo "$(BLUE)Running unit tests...$(NC)"
	cd services/consensus && go test ./...
	cd services/api-gateway && go test ./...

.PHONY: test-integration
test-integration: ## Run integration tests
	@echo "$(BLUE)Running integration tests...$(NC)"
	docker-compose -f docker-compose.test.yml up --abort-on-container-exit
	docker-compose -f docker-compose.test.yml down

.PHONY: test-load
test-load: ## Run load tests
	@echo "$(BLUE)Running load tests...$(NC)"
	kubectl apply -f k8s/testing/load-test-job.yaml
	kubectl wait --for=condition=complete job/avalanche-load-test --timeout=600s
	kubectl logs job/avalanche-load-test

# Deployment targets
.PHONY: deploy
deploy: ## Deploy to Kubernetes
	@echo "$(BLUE)Deploying to Kubernetes...$(NC)"
	./scripts/deploy.sh deploy
	@echo "$(GREEN)Deployment completed$(NC)"

.PHONY: deploy-storage
deploy-storage: ## Deploy storage layer only
	./scripts/deploy.sh storage

.PHONY: deploy-network
deploy-network: ## Deploy network layer only
	./scripts/deploy.sh network

.PHONY: deploy-core
deploy-core: ## Deploy core services only
	./scripts/deploy.sh core

.PHONY: deploy-gateway
deploy-gateway: ## Deploy API gateway only
	./scripts/deploy.sh gateway

.PHONY: deploy-monitoring
deploy-monitoring: ## Deploy monitoring stack only
	./scripts/deploy.sh monitoring

.PHONY: verify
verify: ## Verify deployment status
	./scripts/deploy.sh verify

.PHONY: cleanup
cleanup: ## Remove all deployed resources
	@echo "$(YELLOW)Warning: This will remove all deployed resources$(NC)"
	@read -p "Are you sure? [y/N] " -n 1 -r; \
	echo; \
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then \
		./scripts/deploy.sh cleanup; \
		echo "$(GREEN)Cleanup completed$(NC)"; \
	else \
		echo "$(BLUE)Cleanup cancelled$(NC)"; \
	fi

# Kubernetes management targets
.PHONY: k8s-status
k8s-status: ## Show Kubernetes cluster status
	@echo "$(BLUE)Kubernetes cluster status:$(NC)"
	kubectl cluster-info
	kubectl get nodes
	kubectl get namespaces | grep $(NAMESPACE_PREFIX)

.PHONY: k8s-pods
k8s-pods: ## Show all pods
	kubectl get pods -A | grep $(NAMESPACE_PREFIX)

.PHONY: k8s-services
k8s-services: ## Show all services
	kubectl get svc -A | grep $(NAMESPACE_PREFIX)

.PHONY: k8s-logs
k8s-logs: ## Show logs from consensus service
	kubectl logs -f deployment/consensus-service -n $(NAMESPACE_PREFIX)-core

.PHONY: k8s-describe
k8s-describe: ## Describe consensus service deployment
	kubectl describe deployment consensus-service -n $(NAMESPACE_PREFIX)-core

# Monitoring targets
.PHONY: prometheus
prometheus: ## Port-forward to Prometheus
	@echo "$(BLUE)Port-forwarding to Prometheus...$(NC)"
	@echo "Access Prometheus at: http://localhost:9090"
	kubectl port-forward svc/prometheus 9090:9090 -n $(NAMESPACE_PREFIX)-monitoring

.PHONY: grafana
grafana: ## Port-forward to Grafana
	@echo "$(BLUE)Port-forwarding to Grafana...$(NC)"
	@echo "Access Grafana at: http://localhost:3000"
	kubectl port-forward svc/grafana 3000:3000 -n $(NAMESPACE_PREFIX)-monitoring

.PHONY: jaeger
jaeger: ## Port-forward to Jaeger
	@echo "$(BLUE)Port-forwarding to Jaeger...$(NC)"
	@echo "Access Jaeger at: http://localhost:16686"
	kubectl port-forward svc/jaeger 16686:16686 -n $(NAMESPACE_PREFIX)-monitoring

.PHONY: api-gateway
api-gateway: ## Port-forward to API Gateway
	@echo "$(BLUE)Port-forwarding to API Gateway...$(NC)"
	@echo "Access API Gateway at: http://localhost:8000"
	kubectl port-forward svc/api-gateway 8000:8000 -n $(NAMESPACE_PREFIX)-gateway

# Database management targets
.PHONY: db-connect
db-connect: ## Connect to state database
	kubectl exec -it deployment/state-database -n $(NAMESPACE_PREFIX)-storage -- psql -U postgres -d avalanche_state

.PHONY: db-backup
db-backup: ## Backup state database
	@echo "$(BLUE)Creating database backup...$(NC)"
	kubectl exec deployment/state-database -n $(NAMESPACE_PREFIX)-storage -- pg_dump -U postgres avalanche_state > backup-$(shell date +%Y%m%d-%H%M%S).sql
	@echo "$(GREEN)Database backup created$(NC)"

.PHONY: db-restore
db-restore: ## Restore database from backup (requires BACKUP_FILE variable)
	@if [ -z "$(BACKUP_FILE)" ]; then \
		echo "$(RED)Error: BACKUP_FILE variable is required$(NC)"; \
		echo "Usage: make db-restore BACKUP_FILE=backup-20231201-120000.sql"; \
		exit 1; \
	fi
	@echo "$(BLUE)Restoring database from $(BACKUP_FILE)...$(NC)"
	kubectl exec -i deployment/state-database -n $(NAMESPACE_PREFIX)-storage -- psql -U postgres avalanche_state < $(BACKUP_FILE)
	@echo "$(GREEN)Database restored$(NC)"

# Security targets
.PHONY: security-scan
security-scan: ## Run security scans on images
	@echo "$(BLUE)Running security scans...$(NC)"
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
		aquasec/trivy image $(DOCKER_REGISTRY)/consensus-service:$(IMAGE_TAG)
	docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
		aquasec/trivy image $(DOCKER_REGISTRY)/api-gateway:$(IMAGE_TAG)

.PHONY: k8s-security
k8s-security: ## Run Kubernetes security checks
	@echo "$(BLUE)Running Kubernetes security checks...$(NC)"
	kubectl auth can-i --list
	kubectl get networkpolicies -A
	kubectl get podsecuritypolicies -A

# Performance targets
.PHONY: benchmark
benchmark: ## Run performance benchmarks
	@echo "$(BLUE)Running performance benchmarks...$(NC)"
	kubectl apply -f k8s/testing/benchmark-job.yaml
	kubectl wait --for=condition=complete job/avalanche-benchmark --timeout=600s
	kubectl logs job/avalanche-benchmark

.PHONY: stress-test
stress-test: ## Run stress tests
	@echo "$(BLUE)Running stress tests...$(NC)"
	kubectl apply -f k8s/testing/stress-test-job.yaml
	kubectl wait --for=condition=complete job/avalanche-stress-test --timeout=1200s
	kubectl logs job/avalanche-stress-test

# Utility targets
.PHONY: format
format: ## Format Go code
	@echo "$(BLUE)Formatting Go code...$(NC)"
	find services -name "*.go" -exec gofmt -w {} \;
	find services -name "*.go" -exec goimports -w {} \;

.PHONY: lint
lint: ## Run linters
	@echo "$(BLUE)Running linters...$(NC)"
	cd services/consensus && golangci-lint run
	cd services/api-gateway && golangci-lint run

.PHONY: deps
deps: ## Update dependencies
	@echo "$(BLUE)Updating dependencies...$(NC)"
	cd services/consensus && go mod tidy && go mod download
	cd services/api-gateway && go mod tidy && go mod download

.PHONY: generate
generate: ## Generate code (protobuf, mocks, etc.)
	@echo "$(BLUE)Generating code...$(NC)"
	cd services/consensus && go generate ./...
	cd services/api-gateway && go generate ./...

.PHONY: docs
docs: ## Generate documentation
	@echo "$(BLUE)Generating documentation...$(NC)"
	cd services/consensus && godoc -http=:6060 &
	@echo "$(GREEN)Documentation server started at http://localhost:6060$(NC)"

# Environment-specific targets
.PHONY: staging
staging: ## Deploy to staging environment
	$(MAKE) deploy ENVIRONMENT=staging

.PHONY: production
production: ## Deploy to production environment
	$(MAKE) deploy ENVIRONMENT=production

# Quick development workflow
.PHONY: quick-dev
quick-dev: ## Quick development setup (build + dev-up)
	$(MAKE) build
	$(MAKE) dev-up

.PHONY: quick-deploy
quick-deploy: ## Quick deployment (build + deploy)
	$(MAKE) build
	$(MAKE) deploy

.PHONY: full-test
full-test: ## Full test suite (unit + integration + load)
	$(MAKE) test
	$(MAKE) test-load

# Information targets
.PHONY: info
info: ## Show deployment information
	./scripts/deploy.sh info

.PHONY: version
version: ## Show version information
	@echo "$(BLUE)AvalancheGo Microservices$(NC)"
	@echo "Version: $(IMAGE_TAG)"
	@echo "Registry: $(DOCKER_REGISTRY)"
	@echo "Environment: $(ENVIRONMENT)"
	@echo "Namespace Prefix: $(NAMESPACE_PREFIX)"

# Maintenance targets
.PHONY: update-images
update-images: ## Update all base images
	@echo "$(BLUE)Updating base images...$(NC)"
	docker pull golang:1.21-alpine
	docker pull alpine:latest
	docker pull postgres:15-alpine
	docker pull redis:7-alpine
	docker pull prom/prometheus:latest

.PHONY: prune
prune: ## Clean up unused Docker resources
	@echo "$(BLUE)Cleaning up Docker resources...$(NC)"
	docker system prune -f
	docker volume prune -f
	docker network prune -f

# CI/CD targets
.PHONY: ci-build
ci-build: ## CI build pipeline
	$(MAKE) deps
	$(MAKE) format
	$(MAKE) lint
	$(MAKE) test-unit
	$(MAKE) build

.PHONY: ci-deploy
ci-deploy: ## CI deployment pipeline
	$(MAKE) security-scan
	$(MAKE) deploy
	$(MAKE) verify
	$(MAKE) test-integration

# Special targets
.PHONY: all
all: ## Build, test, and deploy everything
	$(MAKE) ci-build
	$(MAKE) ci-deploy
	@echo "$(GREEN)All tasks completed successfully!$(NC)"

# Ensure scripts are executable
scripts/deploy.sh:
	chmod +x scripts/deploy.sh 