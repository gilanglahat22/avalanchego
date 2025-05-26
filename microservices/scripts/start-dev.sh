#!/bin/bash

# AvalancheGo Microservices Development Startup Script
# This script starts all services in the correct order for development

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
COMPOSE_FILE="docker-compose.yml"
SERVICES_ORDER=(
    "message-queue"
    "state-database"
    "block-database"
    "cache-service"
    "consensus-service"
    "vm-manager-service"
    "p2p-network-service"
    "api-gateway"
    "prometheus"
    "grafana"
)

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if Docker is running
check_docker() {
    if ! docker info >/dev/null 2>&1; then
        print_error "Docker is not running. Please start Docker first."
        exit 1
    fi
    print_success "Docker is running"
}

# Function to check if docker-compose is available
check_docker_compose() {
    if ! command -v docker-compose &> /dev/null; then
        print_error "docker-compose is not installed"
        exit 1
    fi
    print_success "docker-compose is available"
}

# Function to build all images
build_images() {
    print_status "Building Docker images..."
    
    # Build consensus service
    if [ -d "services/consensus" ]; then
        print_status "Building consensus service..."
        cd services/consensus
        docker build -t avalanche/consensus-service:latest .
        cd ../..
    fi
    
    # Build API gateway
    if [ -d "services/api-gateway" ]; then
        print_status "Building API gateway..."
        cd services/api-gateway
        docker build -t avalanche/api-gateway:latest .
        cd ../..
    fi
    
    # Build VM manager
    if [ -d "services/vm-manager" ]; then
        print_status "Building VM manager..."
        cd services/vm-manager
        docker build -t avalanche/vm-manager:latest .
        cd ../..
    fi
    
    # Build P2P network
    if [ -d "services/p2p-network" ]; then
        print_status "Building P2P network service..."
        cd services/p2p-network
        docker build -t avalanche/p2p-network:latest .
        cd ../..
    fi
    
    print_success "All images built successfully"
}

# Function to start services in order
start_services() {
    print_status "Starting services in order..."
    
    for service in "${SERVICES_ORDER[@]}"; do
        print_status "Starting $service..."
        docker-compose up -d $service
        
        # Wait for service to be healthy
        wait_for_service $service
    done
    
    print_success "All services started successfully"
}

# Function to wait for a service to be healthy
wait_for_service() {
    local service=$1
    local max_attempts=30
    local attempt=1
    
    print_status "Waiting for $service to be healthy..."
    
    while [ $attempt -le $max_attempts ]; do
        if docker-compose ps $service | grep -q "Up (healthy)"; then
            print_success "$service is healthy"
            return 0
        elif docker-compose ps $service | grep -q "Up"; then
            print_status "$service is up, waiting for health check... (attempt $attempt/$max_attempts)"
        else
            print_warning "$service is not up yet... (attempt $attempt/$max_attempts)"
        fi
        
        sleep 5
        attempt=$((attempt + 1))
    done
    
    print_warning "$service did not become healthy within expected time"
    return 1
}

# Function to show service status
show_status() {
    print_status "Service Status:"
    docker-compose ps
    
    echo ""
    print_status "Service URLs:"
    echo -e "${GREEN}API Gateway:${NC}      http://localhost:8000"
    echo -e "${GREEN}Consensus Service:${NC} http://localhost:8080"
    echo -e "${GREEN}VM Manager:${NC}       http://localhost:8081"
    echo -e "${GREEN}P2P Network:${NC}      http://localhost:8084"
    echo -e "${GREEN}Prometheus:${NC}       http://localhost:9090"
    echo -e "${GREEN}Grafana:${NC}          http://localhost:3000 (admin/admin)"
    echo -e "${GREEN}State Database:${NC}   localhost:5432 (postgres/password)"
    echo -e "${GREEN}Redis:${NC}            localhost:6379"
}

# Function to show logs
show_logs() {
    local service=${1:-""}
    
    if [ -n "$service" ]; then
        print_status "Showing logs for $service..."
        docker-compose logs -f $service
    else
        print_status "Showing logs for all services..."
        docker-compose logs -f
    fi
}

# Function to test services
test_services() {
    print_status "Testing service connectivity..."
    
    # Test API Gateway
    if curl -s http://localhost:8000/health >/dev/null; then
        print_success "API Gateway is responding"
    else
        print_error "API Gateway is not responding"
    fi
    
    # Test Consensus Service
    if curl -s http://localhost:8080/health >/dev/null; then
        print_success "Consensus Service is responding"
    else
        print_error "Consensus Service is not responding"
    fi
    
    # Test VM Manager
    if curl -s http://localhost:8081/health >/dev/null; then
        print_success "VM Manager is responding"
    else
        print_error "VM Manager is not responding"
    fi
    
    # Test P2P Network
    if curl -s http://localhost:8084/health >/dev/null; then
        print_success "P2P Network Service is responding"
    else
        print_error "P2P Network Service is not responding"
    fi
    
    # Test Prometheus
    if curl -s http://localhost:9090/-/healthy >/dev/null; then
        print_success "Prometheus is responding"
    else
        print_error "Prometheus is not responding"
    fi
}

# Function to stop all services
stop_services() {
    print_status "Stopping all services..."
    docker-compose down
    print_success "All services stopped"
}

# Function to clean up everything
cleanup() {
    print_status "Cleaning up..."
    docker-compose down -v --remove-orphans
    docker system prune -f
    print_success "Cleanup completed"
}

# Function to create sample data
create_sample_data() {
    print_status "Creating sample data..."
    
    # Wait a bit for services to be fully ready
    sleep 10
    
    # Create a sample validator
    print_status "Creating sample validator..."
    curl -X POST http://localhost:8080/validators \
        -H "Content-Type: application/json" \
        -d '{
            "node_id": "sample-validator-1",
            "stake": 1000000,
            "start_time": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
            "subnet_id": "default"
        }' || print_warning "Failed to create sample validator"
    
    # Create a sample VM
    print_status "Creating sample VM..."
    curl -X POST http://localhost:8081/vms \
        -H "Content-Type: application/json" \
        -d '{
            "id": "sample-vm-1",
            "chain_id": "sample-chain",
            "vm_type": "platform",
            "config": "{\"test\": true}"
        }' || print_warning "Failed to create sample VM"
    
    print_success "Sample data created"
}

# Main function
main() {
    case "${1:-start}" in
        "start")
            check_docker
            check_docker_compose
            build_images
            start_services
            show_status
            test_services
            create_sample_data
            print_success "AvalancheGo microservices are running!"
            print_status "Use 'docker-compose logs -f' to view logs"
            print_status "Use '$0 stop' to stop all services"
            ;;
        "stop")
            stop_services
            ;;
        "restart")
            stop_services
            sleep 2
            main start
            ;;
        "status")
            show_status
            ;;
        "logs")
            show_logs $2
            ;;
        "test")
            test_services
            ;;
        "build")
            build_images
            ;;
        "clean")
            cleanup
            ;;
        *)
            echo "Usage: $0 {start|stop|restart|status|logs [service]|test|build|clean}"
            echo ""
            echo "Commands:"
            echo "  start    - Start all services (default)"
            echo "  stop     - Stop all services"
            echo "  restart  - Restart all services"
            echo "  status   - Show service status and URLs"
            echo "  logs     - Show logs (optionally for specific service)"
            echo "  test     - Test service connectivity"
            echo "  build    - Build Docker images"
            echo "  clean    - Stop services and clean up volumes"
            exit 1
            ;;
    esac
}

# Run main function
main "$@" 