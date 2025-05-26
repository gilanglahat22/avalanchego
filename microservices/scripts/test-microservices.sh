#!/bin/bash

# AvalancheGo Microservices Test Script
# This script tests all microservices functionality

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
API_GATEWAY_URL="http://localhost:8000"
CONSENSUS_URL="http://localhost:8080"
VM_MANAGER_URL="http://localhost:8081"
P2P_NETWORK_URL="http://localhost:8084"
PROMETHEUS_URL="http://localhost:9090"

# Test counters
TESTS_PASSED=0
TESTS_FAILED=0

# Function to print colored output
print_status() {
    echo -e "${BLUE}[TEST]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

print_error() {
    echo -e "${RED}[FAIL]${NC} $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

# Function to test HTTP endpoint
test_endpoint() {
    local url=$1
    local expected_status=${2:-200}
    local description=$3
    
    print_status "Testing $description: $url"
    
    local response=$(curl -s -w "%{http_code}" -o /tmp/response.json "$url" 2>/dev/null || echo "000")
    
    if [ "$response" = "$expected_status" ]; then
        print_success "$description is working"
        return 0
    else
        print_error "$description failed (HTTP $response)"
        return 1
    fi
}

# Function to test JSON API endpoint
test_json_endpoint() {
    local url=$1
    local description=$2
    local expected_field=$3
    
    print_status "Testing $description: $url"
    
    local response=$(curl -s "$url" 2>/dev/null)
    local status=$?
    
    if [ $status -eq 0 ]; then
        if [ -n "$expected_field" ]; then
            if echo "$response" | jq -e ".$expected_field" >/dev/null 2>&1; then
                print_success "$description is working and returns expected data"
                return 0
            else
                print_error "$description missing expected field: $expected_field"
                return 1
            fi
        else
            print_success "$description is working"
            return 0
        fi
    else
        print_error "$description is not responding"
        return 1
    fi
}

# Function to test POST endpoint
test_post_endpoint() {
    local url=$1
    local data=$2
    local description=$3
    local expected_status=${4:-200}
    
    print_status "Testing $description: POST $url"
    
    local response=$(curl -s -w "%{http_code}" -o /tmp/response.json \
        -X POST \
        -H "Content-Type: application/json" \
        -d "$data" \
        "$url" 2>/dev/null || echo "000")
    
    if [ "$response" = "$expected_status" ] || [ "$response" = "201" ]; then
        print_success "$description is working"
        return 0
    else
        print_error "$description failed (HTTP $response)"
        if [ -f /tmp/response.json ]; then
            cat /tmp/response.json
        fi
        return 1
    fi
}

# Test basic health endpoints
test_health_endpoints() {
    echo ""
    print_status "=== Testing Health Endpoints ==="
    
    test_endpoint "$CONSENSUS_URL/health" 200 "Consensus Service Health"
    test_endpoint "$VM_MANAGER_URL/health" 200 "VM Manager Health"
    test_endpoint "$P2P_NETWORK_URL/health" 200 "P2P Network Health"
    test_endpoint "$API_GATEWAY_URL/health" 200 "API Gateway Health"
    test_endpoint "$PROMETHEUS_URL/-/healthy" 200 "Prometheus Health"
}

# Test readiness endpoints
test_readiness_endpoints() {
    echo ""
    print_status "=== Testing Readiness Endpoints ==="
    
    test_endpoint "$CONSENSUS_URL/ready" 200 "Consensus Service Readiness"
    test_endpoint "$VM_MANAGER_URL/ready" 200 "VM Manager Readiness"
    test_endpoint "$P2P_NETWORK_URL/ready" 200 "P2P Network Readiness"
    test_endpoint "$API_GATEWAY_URL/ready" 200 "API Gateway Readiness"
}

# Test status endpoints
test_status_endpoints() {
    echo ""
    print_status "=== Testing Status Endpoints ==="
    
    test_json_endpoint "$CONSENSUS_URL/status" "Consensus Service Status" "consensus_mode"
    test_json_endpoint "$VM_MANAGER_URL/status" "VM Manager Status" "total_vms"
    test_json_endpoint "$P2P_NETWORK_URL/status" "P2P Network Status" "node_id"
    test_json_endpoint "$API_GATEWAY_URL/status" "API Gateway Status" "gateway"
}

# Test metrics endpoints
test_metrics_endpoints() {
    echo ""
    print_status "=== Testing Metrics Endpoints ==="
    
    test_endpoint "$CONSENSUS_URL/metrics" 200 "Consensus Service Metrics"
    test_endpoint "$VM_MANAGER_URL/metrics" 200 "VM Manager Metrics"
    test_endpoint "$P2P_NETWORK_URL/metrics" 200 "P2P Network Metrics"
    test_endpoint "$API_GATEWAY_URL/metrics" 200 "API Gateway Metrics"
}

# Test consensus service functionality
test_consensus_functionality() {
    echo ""
    print_status "=== Testing Consensus Service Functionality ==="
    
    # Test validator creation
    local validator_data='{
        "node_id": "test-validator-1",
        "stake": 1000000,
        "start_time": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "subnet_id": "test-subnet"
    }'
    
    test_post_endpoint "$CONSENSUS_URL/validators" "$validator_data" "Create Validator"
    
    # Test getting validators
    test_json_endpoint "$CONSENSUS_URL/validators" "Get Validators" "0"
    
    # Test block processing
    local block_data='{
        "id": "test-block-1",
        "parent_id": "genesis",
        "height": 1,
        "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "data": {"test": true}
    }'
    
    test_post_endpoint "$CONSENSUS_URL/block" "$block_data" "Process Block"
}

# Test VM manager functionality
test_vm_manager_functionality() {
    echo ""
    print_status "=== Testing VM Manager Functionality ==="
    
    # Test VM creation
    local vm_data='{
        "id": "test-vm-1",
        "chain_id": "test-chain",
        "vm_type": "platform",
        "config": "{\"test\": true}"
    }'
    
    test_post_endpoint "$VM_MANAGER_URL/vms" "$vm_data" "Create VM"
    
    # Test getting VMs
    test_json_endpoint "$VM_MANAGER_URL/vms" "Get VMs" "0"
    
    # Test starting VM
    test_post_endpoint "$VM_MANAGER_URL/vms/test-vm-1/start" "{}" "Start VM"
    
    # Test stopping VM
    test_post_endpoint "$VM_MANAGER_URL/vms/test-vm-1/stop" "{}" "Stop VM"
}

# Test P2P network functionality
test_p2p_functionality() {
    echo ""
    print_status "=== Testing P2P Network Functionality ==="
    
    # Test getting peers
    test_json_endpoint "$P2P_NETWORK_URL/peers" "Get Peers" ""
}

# Test API Gateway functionality
test_api_gateway_functionality() {
    echo ""
    print_status "=== Testing API Gateway Functionality ==="
    
    # Test routing to consensus service
    test_endpoint "$API_GATEWAY_URL/api/v1/consensus/status" 200 "API Gateway -> Consensus Routing"
    
    # Test routing to VM manager
    test_endpoint "$API_GATEWAY_URL/api/v1/vm/status" 200 "API Gateway -> VM Manager Routing"
}

# Test inter-service communication
test_inter_service_communication() {
    echo ""
    print_status "=== Testing Inter-Service Communication ==="
    
    # Test that services can communicate via Redis
    print_status "Testing Redis connectivity..."
    
    # Check if Redis is accessible from services
    if docker-compose exec -T message-queue redis-cli ping | grep -q "PONG"; then
        print_success "Redis message queue is accessible"
    else
        print_error "Redis message queue is not accessible"
    fi
    
    # Check database connectivity
    if docker-compose exec -T state-database pg_isready -U postgres | grep -q "accepting connections"; then
        print_success "PostgreSQL state database is accessible"
    else
        print_error "PostgreSQL state database is not accessible"
    fi
}

# Test monitoring and observability
test_monitoring() {
    echo ""
    print_status "=== Testing Monitoring and Observability ==="
    
    # Test Prometheus targets
    local targets_response=$(curl -s "$PROMETHEUS_URL/api/v1/targets" 2>/dev/null)
    if echo "$targets_response" | jq -e '.data.activeTargets' >/dev/null 2>&1; then
        local active_targets=$(echo "$targets_response" | jq '.data.activeTargets | length')
        print_success "Prometheus has $active_targets active targets"
    else
        print_error "Prometheus targets API is not working"
    fi
    
    # Test if metrics are being collected
    local metrics_response=$(curl -s "$PROMETHEUS_URL/api/v1/query?query=up" 2>/dev/null)
    if echo "$metrics_response" | jq -e '.data.result' >/dev/null 2>&1; then
        print_success "Prometheus is collecting metrics"
    else
        print_error "Prometheus is not collecting metrics"
    fi
}

# Test load and performance
test_performance() {
    echo ""
    print_status "=== Testing Performance ==="
    
    # Simple load test
    print_status "Running simple load test..."
    
    local start_time=$(date +%s)
    for i in {1..10}; do
        curl -s "$CONSENSUS_URL/health" >/dev/null &
        curl -s "$VM_MANAGER_URL/health" >/dev/null &
        curl -s "$P2P_NETWORK_URL/health" >/dev/null &
    done
    wait
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    if [ $duration -lt 5 ]; then
        print_success "Load test completed in ${duration}s (good performance)"
    else
        print_warning "Load test completed in ${duration}s (consider optimization)"
    fi
}

# Main test function
run_all_tests() {
    echo ""
    print_status "=== Starting AvalancheGo Microservices Tests ==="
    echo ""
    
    # Wait for services to be ready
    print_status "Waiting for services to be ready..."
    sleep 10
    
    # Run all test suites
    test_health_endpoints
    test_readiness_endpoints
    test_status_endpoints
    test_metrics_endpoints
    test_consensus_functionality
    test_vm_manager_functionality
    test_p2p_functionality
    test_api_gateway_functionality
    test_inter_service_communication
    test_monitoring
    test_performance
    
    # Print summary
    echo ""
    print_status "=== Test Summary ==="
    echo -e "${GREEN}Tests Passed: $TESTS_PASSED${NC}"
    echo -e "${RED}Tests Failed: $TESTS_FAILED${NC}"
    
    if [ $TESTS_FAILED -eq 0 ]; then
        echo ""
        print_success "All tests passed! AvalancheGo microservices are working correctly."
        exit 0
    else
        echo ""
        print_error "Some tests failed. Please check the logs and fix the issues."
        exit 1
    fi
}

# Check if jq is available
if ! command -v jq &> /dev/null; then
    print_warning "jq is not installed. Some JSON tests will be skipped."
fi

# Run tests
run_all_tests 