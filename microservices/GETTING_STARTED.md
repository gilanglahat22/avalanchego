# Getting Started with AvalancheGo Microservices

This guide will help you get the AvalancheGo microservices architecture up and running quickly.

## Quick Start

### Prerequisites

- Docker (v20.10+) and Docker Compose (v2.0+)
- Git for cloning the repository
- curl for testing APIs
- jq (optional) for JSON processing in tests

### 1. Start the Microservices

```bash
# Start all services
./scripts/start-dev.sh start

# Or use Docker Compose directly
docker-compose up -d
```

### 2. Verify Everything is Running

```bash
# Check service status
./scripts/start-dev.sh status

# Run comprehensive tests
./scripts/test-microservices.sh
```

### 3. Access the Services

Once running, you can access:

- API Gateway: http://localhost:8000
- Consensus Service: http://localhost:8080
- VM Manager: http://localhost:8081
- P2P Network: http://localhost:8084
- Prometheus: http://localhost:9090
- Grafana: http://localhost:3000 (admin/admin)

## Service Overview

### Core Services

1. Consensus Service (Port 8080)
   - Handles Snowman/Avalanche consensus algorithms
   - Manages validators and block processing
   - Provides consensus status and metrics

2. VM Manager Service (Port 8081)
   - Manages virtual machine instances
   - Handles VM lifecycle (create, start, stop, delete)
   - Tracks VM status and performance

3. P2P Network Service (Port 8084)
   - Manages peer-to-peer networking
   - Handles peer discovery and communication
   - Routes messages between nodes

4. API Gateway (Port 8000)
   - Central entry point for all API requests
   - Handles authentication and rate limiting
   - Routes requests to appropriate services

### Infrastructure Services

- PostgreSQL (Port 5432): State and block databases
- Redis (Port 6379): Message queue and caching
- Prometheus (Port 9090): Metrics collection
- Grafana (Port 3000): Monitoring dashboards 