#!/bin/bash

# AvalancheGo Microservices Deployment Script
# This script deploys the complete microservices architecture to Kubernetes

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
NAMESPACE_PREFIX="avalanche"
DOCKER_REGISTRY="${DOCKER_REGISTRY:-avalanche}"
IMAGE_TAG="${IMAGE_TAG:-v1.0.0}"
ENVIRONMENT="${ENVIRONMENT:-development}"

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

# Function to check if kubectl is available
check_kubectl() {
    if ! command -v kubectl &> /dev/null; then
        print_error "kubectl is not installed or not in PATH"
        exit 1
    fi
    
    if ! kubectl cluster-info &> /dev/null; then
        print_error "Cannot connect to Kubernetes cluster"
        exit 1
    fi
    
    print_success "kubectl is available and connected to cluster"
}

# Function to check if required tools are available
check_dependencies() {
    print_status "Checking dependencies..."
    
    check_kubectl
    
    if ! command -v docker &> /dev/null; then
        print_warning "Docker is not installed. Skipping image build."
        SKIP_BUILD=true
    fi
    
    if ! command -v helm &> /dev/null; then
        print_warning "Helm is not installed. Some features may not be available."
    fi
}

# Function to create namespaces
create_namespaces() {
    print_status "Creating namespaces..."
    
    kubectl apply -f k8s/namespaces.yaml
    
    # Wait for namespaces to be ready
    for ns in core network storage monitoring gateway; do
        kubectl wait --for=condition=Ready namespace/${NAMESPACE_PREFIX}-${ns} --timeout=60s
    done
    
    print_success "Namespaces created successfully"
}

# Function to create secrets
create_secrets() {
    print_status "Creating secrets..."
    
    # Database credentials
    kubectl create secret generic database-credentials \
        --from-literal=username=postgres \
        --from-literal=password=$(openssl rand -base64 32) \
        --namespace=${NAMESPACE_PREFIX}-storage \
        --dry-run=client -o yaml | kubectl apply -f -
    
    # API Gateway secrets
    kubectl create secret generic api-gateway-secrets \
        --from-literal=jwt-secret=$(openssl rand -base64 64) \
        --namespace=${NAMESPACE_PREFIX}-gateway \
        --dry-run=client -o yaml | kubectl apply -f -
    
    # TLS certificates (self-signed for development)
    if [ "$ENVIRONMENT" = "development" ]; then
        openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
            -keyout /tmp/tls.key -out /tmp/tls.crt \
            -subj "/CN=api.avalanche.local/O=avalanche"
        
        kubectl create secret tls api-gateway-tls \
            --cert=/tmp/tls.crt --key=/tmp/tls.key \
            --namespace=${NAMESPACE_PREFIX}-gateway \
            --dry-run=client -o yaml | kubectl apply -f -
        
        rm -f /tmp/tls.key /tmp/tls.crt
    fi
    
    print_success "Secrets created successfully"
}

# Function to build Docker images
build_images() {
    if [ "$SKIP_BUILD" = "true" ]; then
        print_warning "Skipping image build"
        return
    fi
    
    print_status "Building Docker images..."
    
    services=("consensus" "api-gateway" "vm-manager" "p2p-network" "chain-manager" "validator")
    
    for service in "${services[@]}"; do
        if [ -d "services/$service" ]; then
            print_status "Building $service service..."
            docker build -t ${DOCKER_REGISTRY}/${service}:${IMAGE_TAG} services/${service}/
            
            # Push to registry if not local development
            if [ "$ENVIRONMENT" != "development" ]; then
                docker push ${DOCKER_REGISTRY}/${service}:${IMAGE_TAG}
            fi
        else
            print_warning "Service directory services/$service not found, skipping..."
        fi
    done
    
    print_success "Docker images built successfully"
}

# Function to deploy storage layer
deploy_storage() {
    print_status "Deploying storage layer..."
    
    kubectl apply -f k8s/storage/
    
    # Wait for databases to be ready
    kubectl wait --for=condition=Ready pod -l app=state-database \
        --namespace=${NAMESPACE_PREFIX}-storage --timeout=300s
    
    print_success "Storage layer deployed successfully"
}

# Function to deploy network layer
deploy_network() {
    print_status "Deploying network layer..."
    
    kubectl apply -f k8s/network/
    
    # Wait for network services to be ready
    kubectl wait --for=condition=Ready pod -l app=p2p-network-service \
        --namespace=${NAMESPACE_PREFIX}-network --timeout=180s
    
    print_success "Network layer deployed successfully"
}

# Function to deploy core services
deploy_core() {
    print_status "Deploying core services..."
    
    kubectl apply -f k8s/core/
    
    # Wait for core services to be ready
    kubectl wait --for=condition=Ready pod -l app=consensus-service \
        --namespace=${NAMESPACE_PREFIX}-core --timeout=180s
    
    kubectl wait --for=condition=Ready pod -l app=vm-manager-service \
        --namespace=${NAMESPACE_PREFIX}-core --timeout=180s
    
    print_success "Core services deployed successfully"
}

# Function to deploy gateway
deploy_gateway() {
    print_status "Deploying API gateway..."
    
    kubectl apply -f k8s/gateway/
    
    # Wait for gateway to be ready
    kubectl wait --for=condition=Ready pod -l app=api-gateway \
        --namespace=${NAMESPACE_PREFIX}-gateway --timeout=180s
    
    print_success "API gateway deployed successfully"
}

# Function to deploy monitoring
deploy_monitoring() {
    print_status "Deploying monitoring stack..."
    
    kubectl apply -f k8s/monitoring/
    
    # Wait for Prometheus to be ready
    kubectl wait --for=condition=Ready pod -l app=prometheus \
        --namespace=${NAMESPACE_PREFIX}-monitoring --timeout=180s
    
    print_success "Monitoring stack deployed successfully"
}

# Function to setup Istio service mesh
setup_istio() {
    if ! command -v istioctl &> /dev/null; then
        print_warning "istioctl not found, skipping Istio setup"
        return
    fi
    
    print_status "Setting up Istio service mesh..."
    
    # Install Istio if not already installed
    if ! kubectl get namespace istio-system &> /dev/null; then
        istioctl install --set values.defaultRevision=default -y
    fi
    
    # Enable Istio injection for namespaces
    for ns in core network gateway; do
        kubectl label namespace ${NAMESPACE_PREFIX}-${ns} istio-injection=enabled --overwrite
    done
    
    print_success "Istio service mesh configured"
}

# Function to verify deployment
verify_deployment() {
    print_status "Verifying deployment..."
    
    # Check all pods are running
    for ns in core network storage monitoring gateway; do
        print_status "Checking namespace: ${NAMESPACE_PREFIX}-${ns}"
        kubectl get pods -n ${NAMESPACE_PREFIX}-${ns}
        
        # Check if any pods are not running
        if kubectl get pods -n ${NAMESPACE_PREFIX}-${ns} --field-selector=status.phase!=Running --no-headers | grep -q .; then
            print_warning "Some pods in ${NAMESPACE_PREFIX}-${ns} are not running"
        fi
    done
    
    # Check services
    print_status "Checking services..."
    kubectl get svc -A | grep ${NAMESPACE_PREFIX}
    
    # Check ingress/gateway
    if kubectl get gateway -n ${NAMESPACE_PREFIX}-gateway &> /dev/null; then
        print_status "Istio Gateway status:"
        kubectl get gateway -n ${NAMESPACE_PREFIX}-gateway
    fi
    
    print_success "Deployment verification completed"
}

# Function to get access information
get_access_info() {
    print_status "Getting access information..."
    
    # API Gateway access
    if kubectl get svc api-gateway -n ${NAMESPACE_PREFIX}-gateway &> /dev/null; then
        GATEWAY_IP=$(kubectl get svc api-gateway -n ${NAMESPACE_PREFIX}-gateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
        GATEWAY_PORT=$(kubectl get svc api-gateway -n ${NAMESPACE_PREFIX}-gateway -o jsonpath='{.spec.ports[0].port}')
        
        if [ -n "$GATEWAY_IP" ]; then
            echo -e "${GREEN}API Gateway:${NC} http://${GATEWAY_IP}:${GATEWAY_PORT}"
        else
            echo -e "${YELLOW}API Gateway:${NC} Use port-forward: kubectl port-forward svc/api-gateway 8000:8000 -n ${NAMESPACE_PREFIX}-gateway"
        fi
    fi
    
    # Prometheus access
    if kubectl get svc prometheus -n ${NAMESPACE_PREFIX}-monitoring &> /dev/null; then
        echo -e "${GREEN}Prometheus:${NC} Use port-forward: kubectl port-forward svc/prometheus 9090:9090 -n ${NAMESPACE_PREFIX}-monitoring"
    fi
    
    # Grafana access (if deployed)
    if kubectl get svc grafana -n ${NAMESPACE_PREFIX}-monitoring &> /dev/null; then
        echo -e "${GREEN}Grafana:${NC} Use port-forward: kubectl port-forward svc/grafana 3000:3000 -n ${NAMESPACE_PREFIX}-monitoring"
    fi
}

# Function to cleanup deployment
cleanup() {
    print_status "Cleaning up deployment..."
    
    # Delete all resources
    kubectl delete -f k8s/gateway/ --ignore-not-found=true
    kubectl delete -f k8s/core/ --ignore-not-found=true
    kubectl delete -f k8s/network/ --ignore-not-found=true
    kubectl delete -f k8s/storage/ --ignore-not-found=true
    kubectl delete -f k8s/monitoring/ --ignore-not-found=true
    
    # Delete namespaces
    kubectl delete -f k8s/namespaces.yaml --ignore-not-found=true
    
    print_success "Cleanup completed"
}

# Main deployment function
deploy_all() {
    print_status "Starting AvalancheGo microservices deployment..."
    
    check_dependencies
    create_namespaces
    create_secrets
    build_images
    deploy_storage
    deploy_network
    deploy_core
    deploy_gateway
    deploy_monitoring
    setup_istio
    verify_deployment
    get_access_info
    
    print_success "Deployment completed successfully!"
}

# Parse command line arguments
case "${1:-deploy}" in
    "deploy")
        deploy_all
        ;;
    "cleanup")
        cleanup
        ;;
    "verify")
        verify_deployment
        ;;
    "build")
        build_images
        ;;
    "storage")
        deploy_storage
        ;;
    "network")
        deploy_network
        ;;
    "core")
        deploy_core
        ;;
    "gateway")
        deploy_gateway
        ;;
    "monitoring")
        deploy_monitoring
        ;;
    "istio")
        setup_istio
        ;;
    "info")
        get_access_info
        ;;
    *)
        echo "Usage: $0 {deploy|cleanup|verify|build|storage|network|core|gateway|monitoring|istio|info}"
        echo ""
        echo "Commands:"
        echo "  deploy     - Deploy all components (default)"
        echo "  cleanup    - Remove all deployed components"
        echo "  verify     - Verify deployment status"
        echo "  build      - Build Docker images only"
        echo "  storage    - Deploy storage layer only"
        echo "  network    - Deploy network layer only"
        echo "  core       - Deploy core services only"
        echo "  gateway    - Deploy API gateway only"
        echo "  monitoring - Deploy monitoring stack only"
        echo "  istio      - Setup Istio service mesh only"
        echo "  info       - Show access information"
        echo ""
        echo "Environment variables:"
        echo "  DOCKER_REGISTRY - Docker registry prefix (default: avalanche)"
        echo "  IMAGE_TAG       - Docker image tag (default: v1.0.0)"
        echo "  ENVIRONMENT     - Deployment environment (default: development)"
        exit 1
        ;;
esac 