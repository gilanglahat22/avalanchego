# AvalancheGo Microservices Implementation

A complete microservices architecture implementation of AvalancheGo using Kubernetes, designed for high scalability, parallel processing, and production-ready deployment.

## 🏗️ Architecture Overview

This implementation decomposes the monolithic AvalancheGo into a distributed microservices architecture with the following components:

### Core Services
- **Consensus Service** - Handles Snowman/Avalanche consensus algorithms
- **VM Manager Service** - Manages virtual machine instances and execution
- **Chain Manager Service** - Manages blockchain chains and state
- **Validator Service** - Handles validator management and staking

### Network Services
- **P2P Network Service** - Peer-to-peer networking and discovery
- **Message Router Service** - Inter-service message routing
- **Peer Manager Service** - Peer connection management

### Data Services
- **State Database** - PostgreSQL cluster for blockchain state
- **Block Database** - Separate database for block storage
- **Indexer Service** - Blockchain data indexing and querying
- **Cache Service** - Redis-based caching layer

### Gateway & Infrastructure
- **API Gateway** - Central entry point with authentication and routing
- **Auth Service** - JWT-based authentication
- **Config Service** - Centralized configuration management
- **Health Service** - Health monitoring and status reporting

## 🏛️ Architecture Diagrams

### System Architecture Overview

```mermaid
graph TB
    %% External Layer
    Client[Client Applications]
    LoadBalancer[Load Balancer]
    
    %% Gateway Layer
    subgraph "Gateway Layer"
        APIGateway[API Gateway<br/>:8000]
        AuthService[Auth Service<br/>:8088]
    end
    
    %% Core Services Layer
    subgraph "Core Blockchain Services"
        ConsensusService[Consensus Service<br/>:8080]
        VMManager[VM Manager<br/>:8081]
        ChainManager[Chain Manager<br/>:8082]
        ValidatorService[Validator Service<br/>:8083]
    end
    
    %% Network Layer
    subgraph "Network Services"
        P2PNetwork[P2P Network<br/>:8084]
        MessageRouter[Message Router<br/>:8085]
        PeerManager[Peer Manager<br/>:8086]
    end
    
    %% Data Layer
    subgraph "Data Services"
        IndexerService[Indexer Service<br/>:8087]
        StateDB[(State Database<br/>PostgreSQL)]
        BlockDB[(Block Database<br/>PostgreSQL)]
        Redis[(Redis Cache<br/>& Message Queue)]
    end
    
    %% Infrastructure Services
    subgraph "Infrastructure Services"
        APIService[API Service<br/>:8089]
        HealthService[Health Service<br/>:8090]
        MetricsService[Metrics Service<br/>:8091]
        ConfigService[Config Service<br/>:8092]
    end
    
    %% Monitoring Stack
    subgraph "Monitoring & Observability"
        Prometheus[Prometheus<br/>:9090]
        Grafana[Grafana<br/>:3000]
        Jaeger[Jaeger<br/>:16686]
    end
    
    %% External Connections
    Client --> LoadBalancer
    LoadBalancer --> APIGateway
    
    %% Gateway Layer Connections
    APIGateway --> AuthService
    APIGateway --> APIService
    
    %% API Service to Core Services
    APIService --> ConsensusService
    APIService --> VMManager
    APIService --> ChainManager
    APIService --> ValidatorService
    
    %% Core Services Interconnections
    ConsensusService --> VMManager
    ConsensusService --> ChainManager
    ValidatorService --> ConsensusService
    ChainManager --> VMManager
    
    %% Network Layer Connections
    P2PNetwork --> MessageRouter
    MessageRouter --> PeerManager
    P2PNetwork --> PeerManager
    
    %% Core to Network Connections
    ConsensusService --> MessageRouter
    ValidatorService --> P2PNetwork
    
    %% Data Layer Connections
    ConsensusService --> StateDB
    ChainManager --> BlockDB
    IndexerService --> StateDB
    IndexerService --> BlockDB
    
    %% Redis Connections
    AuthService --> Redis
    MessageRouter --> Redis
    PeerManager --> Redis
    
    %% Infrastructure Connections
    HealthService --> ConsensusService
    HealthService --> VMManager
    HealthService --> ChainManager
    MetricsService --> Prometheus
    
    %% Monitoring Connections
    Prometheus --> Grafana
    
    %% All services expose metrics to Prometheus
    ConsensusService -.-> Prometheus
    VMManager -.-> Prometheus
    ChainManager -.-> Prometheus
    ValidatorService -.-> Prometheus
    P2PNetwork -.-> Prometheus
    MessageRouter -.-> Prometheus
    PeerManager -.-> Prometheus
    IndexerService -.-> Prometheus
    APIGateway -.-> Prometheus
    AuthService -.-> Prometheus
    
    %% Tracing connections
    ConsensusService -.-> Jaeger
    VMManager -.-> Jaeger
    ChainManager -.-> Jaeger
    APIGateway -.-> Jaeger
```

### Data Flow Architecture

```mermaid
graph LR
    %% Transaction Flow
    subgraph "Transaction Processing Flow"
        TxSubmit[Transaction<br/>Submission]
        TxValidation[Transaction<br/>Validation]
        TxExecution[Transaction<br/>Execution]
        TxConsensus[Consensus<br/>Processing]
        TxFinalization[Block<br/>Finalization]
        TxIndexing[Data<br/>Indexing]
    end
    
    %% Service Mapping
    TxSubmit --> APIGateway
    APIGateway --> TxValidation
    TxValidation --> VMManager
    VMManager --> TxExecution
    TxExecution --> ConsensusService
    ConsensusService --> TxConsensus
    TxConsensus --> ChainManager
    ChainManager --> TxFinalization
    TxFinalization --> IndexerService
    IndexerService --> TxIndexing
    
    %% Data Storage Flow
    TxFinalization --> StateDB
    TxFinalization --> BlockDB
    TxIndexing --> StateDB
```

### Service Communication Flow

```mermaid
sequenceDiagram
    participant Client
    participant APIGateway
    participant AuthService
    participant APIService
    participant ConsensusService
    participant VMManager
    participant ChainManager
    participant StateDB
    participant P2PNetwork
    
    %% Authentication Flow
    Client->>APIGateway: Request with JWT
    APIGateway->>AuthService: Validate Token
    AuthService-->>APIGateway: Token Valid
    
    %% Transaction Processing Flow
    APIGateway->>APIService: Forward Request
    APIService->>ConsensusService: Submit Transaction
    ConsensusService->>VMManager: Execute Transaction
    VMManager-->>ConsensusService: Execution Result
    ConsensusService->>ChainManager: Propose Block
    ChainManager->>StateDB: Update State
    StateDB-->>ChainManager: State Updated
    ChainManager-->>ConsensusService: Block Committed
    
    %% Network Propagation
    ConsensusService->>P2PNetwork: Broadcast Block
    P2PNetwork-->>ConsensusService: Propagation Complete
    
    %% Response Flow
    ConsensusService-->>APIService: Transaction Result
    APIService-->>APIGateway: Response
    APIGateway-->>Client: Final Response
```

### Consensus Algorithm Flow

```mermaid
graph TD
    %% Snowman Consensus Flow
    subgraph "Snowman Consensus Algorithm"
        BlockProposal[Block Proposal]
        ValidatorQuery[Query Validators]
        VoteCollection[Collect Votes]
        ConfidenceCheck{Confidence >= Threshold?}
        BlockAccept[Accept Block]
        BlockReject[Reject Block]
        StateUpdate[Update State]
    end
    
    BlockProposal --> ValidatorQuery
    ValidatorQuery --> VoteCollection
    VoteCollection --> ConfidenceCheck
    ConfidenceCheck -->|Yes| BlockAccept
    ConfidenceCheck -->|No| BlockReject
    BlockAccept --> StateUpdate
    BlockReject --> BlockProposal
    
    %% Service Integration
    ConsensusService --> BlockProposal
    ValidatorService --> ValidatorQuery
    P2PNetwork --> VoteCollection
    ChainManager --> StateUpdate
```

### Network Topology

```mermaid
graph TB
    %% Kubernetes Namespaces
    subgraph "avalanche-gateway"
        GW_APIGateway[API Gateway]
        GW_AuthService[Auth Service]
        GW_APIService[API Service]
    end
    
    subgraph "avalanche-core"
        CORE_Consensus[Consensus Service]
        CORE_VMManager[VM Manager]
        CORE_ChainManager[Chain Manager]
        CORE_Validator[Validator Service]
    end
    
    subgraph "avalanche-network"
        NET_P2P[P2P Network]
        NET_MessageRouter[Message Router]
        NET_PeerManager[Peer Manager]
    end
    
    subgraph "avalanche-storage"
        STOR_StateDB[State Database]
        STOR_BlockDB[Block Database]
        STOR_Redis[Redis Cluster]
        STOR_Indexer[Indexer Service]
    end
    
    subgraph "avalanche-infrastructure"
        INFRA_Health[Health Service]
        INFRA_Metrics[Metrics Service]
        INFRA_Config[Config Service]
    end
    
    subgraph "avalanche-monitoring"
        MON_Prometheus[Prometheus]
        MON_Grafana[Grafana]
        MON_Jaeger[Jaeger]
    end
    
    %% Inter-namespace communication
    GW_APIGateway --> CORE_Consensus
    GW_APIService --> CORE_VMManager
    CORE_Consensus --> NET_MessageRouter
    CORE_ChainManager --> STOR_StateDB
    INFRA_Health --> CORE_Consensus
    MON_Prometheus --> CORE_Consensus
```

### Deployment Architecture

```mermaid
graph TB
    %% Infrastructure Layer
    subgraph "Infrastructure Layer"
        K8s[Kubernetes Cluster]
        Istio[Istio Service Mesh]
        Ingress[Ingress Controller]
    end
    
    %% Application Layer
    subgraph "Application Layer"
        Microservices[Microservices]
        Databases[Databases]
        Cache[Cache Layer]
    end
    
    %% Monitoring Layer
    subgraph "Observability Layer"
        Metrics[Metrics Collection]
        Logging[Centralized Logging]
        Tracing[Distributed Tracing]
    end
    
    %% Security Layer
    subgraph "Security Layer"
        RBAC[RBAC Policies]
        NetworkPolicies[Network Policies]
        PodSecurity[Pod Security Standards]
    end
    
    %% External Access
    Internet[Internet] --> Ingress
    Ingress --> Istio
    Istio --> Microservices
    
    %% Internal Dependencies
    Microservices --> Databases
    Microservices --> Cache
    Microservices --> Metrics
    Microservices --> Logging
    Microservices --> Tracing
    
    %% Security Integration
    RBAC --> Microservices
    NetworkPolicies --> Microservices
    PodSecurity --> Microservices
```

### Scaling Strategy

```mermaid
graph LR
    %% Load Patterns
    subgraph "Load Patterns"
        LowLoad[Low Load<br/>1-10 TPS]
        MediumLoad[Medium Load<br/>100-1K TPS]
        HighLoad[High Load<br/>10K+ TPS]
    end
    
    %% Scaling Responses
    subgraph "Scaling Responses"
        MinReplicas[Min Replicas<br/>1-2 pods]
        StandardReplicas[Standard Replicas<br/>3-5 pods]
        MaxReplicas[Max Replicas<br/>10-20 pods]
    end
    
    %% Auto-scaling Triggers
    subgraph "HPA Triggers"
        CPUMetric[CPU > 70%]
        MemoryMetric[Memory > 80%]
        CustomMetric[Custom Metrics<br/>TPS, Queue Length]
    end
    
    LowLoad --> MinReplicas
    MediumLoad --> StandardReplicas
    HighLoad --> MaxReplicas
    
    CPUMetric --> StandardReplicas
    MemoryMetric --> StandardReplicas
         CustomMetric --> MaxReplicas
```

## 🔄 Flow Logic Schemas

### Transaction Processing Flow Logic

```mermaid
flowchart TD
    Start([Transaction Received]) --> Auth{Authentication<br/>Valid?}
    Auth -->|No| AuthError[Return 401<br/>Unauthorized]
    Auth -->|Yes| RateLimit{Rate Limit<br/>Exceeded?}
    RateLimit -->|Yes| RateLimitError[Return 429<br/>Too Many Requests]
    RateLimit -->|No| ValidateTx{Transaction<br/>Valid?}
    
    ValidateTx -->|No| ValidationError[Return 400<br/>Bad Request]
    ValidateTx -->|Yes| CheckBalance{Sufficient<br/>Balance?}
    CheckBalance -->|No| BalanceError[Return 400<br/>Insufficient Funds]
    CheckBalance -->|Yes| ExecuteTx[Execute Transaction<br/>in VM]
    
    ExecuteTx --> ExecResult{Execution<br/>Successful?}
    ExecResult -->|No| ExecError[Return 500<br/>Execution Failed]
    ExecResult -->|Yes| ProposeBlock[Propose Block<br/>for Consensus]
    
    ProposeBlock --> Consensus{Consensus<br/>Achieved?}
    Consensus -->|No| ConsensusRetry[Retry Consensus<br/>Process]
    ConsensusRetry --> Consensus
    Consensus -->|Yes| CommitBlock[Commit Block<br/>to Chain]
    
    CommitBlock --> UpdateState[Update State<br/>Database]
    UpdateState --> IndexData[Index Transaction<br/>Data]
    IndexData --> BroadcastBlock[Broadcast Block<br/>to Network]
    BroadcastBlock --> Success[Return 200<br/>Transaction Success]
    
    %% Error Handling
    AuthError --> End([End])
    RateLimitError --> End
    ValidationError --> End
    BalanceError --> End
    ExecError --> End
    Success --> End
```

### Service Health Check Flow

```mermaid
flowchart TD
    HealthCheck([Health Check Initiated]) --> CheckRedis{Redis<br/>Available?}
    CheckRedis -->|No| RedisDown[Mark Redis<br/>Unhealthy]
    CheckRedis -->|Yes| CheckDB{Database<br/>Available?}
    
    CheckDB -->|No| DBDown[Mark Database<br/>Unhealthy]
    CheckDB -->|Yes| CheckDependencies{Dependent Services<br/>Healthy?}
    
    CheckDependencies -->|No| DepsDown[Mark Dependencies<br/>Unhealthy]
    CheckDependencies -->|Yes| CheckResources{Resource Usage<br/>Normal?}
    
    CheckResources -->|No| ResourceIssue[Mark Resources<br/>Warning]
    CheckResources -->|Yes| Healthy[Mark Service<br/>Healthy]
    
    RedisDown --> UpdateMetrics[Update Prometheus<br/>Metrics]
    DBDown --> UpdateMetrics
    DepsDown --> UpdateMetrics
    ResourceIssue --> UpdateMetrics
    Healthy --> UpdateMetrics
    
    UpdateMetrics --> NotifyMonitoring[Notify Monitoring<br/>Systems]
    NotifyMonitoring --> End([End])
```

### Consensus Decision Flow

```mermaid
flowchart TD
    BlockProposed([Block Proposed]) --> ValidateBlock{Block<br/>Valid?}
    ValidateBlock -->|No| RejectBlock[Reject Block]
    ValidateBlock -->|Yes| QueryValidators[Query Network<br/>Validators]
    
    QueryValidators --> CollectVotes[Collect Validator<br/>Votes]
    CollectVotes --> CheckTimeout{Timeout<br/>Reached?}
    CheckTimeout -->|Yes| TimeoutHandler[Handle Timeout<br/>Scenario]
    CheckTimeout -->|No| CountVotes[Count Positive<br/>Votes]
    
    CountVotes --> CheckThreshold{Votes >= Threshold<br/>({threshold}%)?}
    CheckThreshold -->|No| CheckRounds{Max Rounds<br/>Reached?}
    CheckRounds -->|Yes| RejectBlock
    CheckRounds -->|No| NextRound[Start Next<br/>Consensus Round]
    NextRound --> QueryValidators
    
    CheckThreshold -->|Yes| AcceptBlock[Accept Block]
    AcceptBlock --> UpdateChain[Update Blockchain<br/>State]
    UpdateChain --> NotifyNetwork[Notify Network<br/>of Decision]
    
    RejectBlock --> LogRejection[Log Rejection<br/>Reason]
    TimeoutHandler --> LogTimeout[Log Timeout<br/>Event]
    
    LogRejection --> End([End])
    LogTimeout --> End
    NotifyNetwork --> End
```

### Auto-Scaling Decision Flow

```mermaid
flowchart TD
    MetricsCheck([Metrics Collection]) --> CheckCPU{CPU Usage<br/>> 70%?}
    CheckCPU -->|Yes| CPUScale[Trigger CPU-based<br/>Scaling]
    CheckCPU -->|No| CheckMemory{Memory Usage<br/>> 80%?}
    
    CheckMemory -->|Yes| MemoryScale[Trigger Memory-based<br/>Scaling]
    CheckMemory -->|No| CheckCustom{Custom Metrics<br/>Threshold Exceeded?}
    
    CheckCustom -->|Yes| CustomScale[Trigger Custom<br/>Metric Scaling]
    CheckCustom -->|No| CheckDownscale{Resource Usage<br/>< 30% for 5min?}
    
    CheckDownscale -->|Yes| DownscaleCheck{Current Replicas<br/>> Min Replicas?}
    DownscaleCheck -->|Yes| Downscale[Scale Down<br/>Replicas]
    DownscaleCheck -->|No| NoAction[No Scaling<br/>Action]
    CheckDownscale -->|No| NoAction
    
    CPUScale --> CheckMaxReplicas{Current Replicas<br/>< Max Replicas?}
    MemoryScale --> CheckMaxReplicas
    CustomScale --> CheckMaxReplicas
    
    CheckMaxReplicas -->|Yes| ScaleUp[Scale Up<br/>Replicas]
    CheckMaxReplicas -->|No| MaxReached[Max Replicas<br/>Reached]
    
    ScaleUp --> UpdateMetrics[Update Scaling<br/>Metrics]
    Downscale --> UpdateMetrics
    MaxReached --> UpdateMetrics
    NoAction --> UpdateMetrics
    
    UpdateMetrics --> End([End])
```

### Error Handling and Recovery Flow

```mermaid
flowchart TD
    ErrorDetected([Error Detected]) --> ClassifyError{Error<br/>Type?}
    
    ClassifyError -->|Network| NetworkError[Network Error<br/>Handling]
    ClassifyError -->|Database| DatabaseError[Database Error<br/>Handling]
    ClassifyError -->|Service| ServiceError[Service Error<br/>Handling]
    ClassifyError -->|Consensus| ConsensusError[Consensus Error<br/>Handling]
    
    NetworkError --> RetryNetwork{Retry<br/>Attempts < 3?}
    RetryNetwork -->|Yes| WaitAndRetry[Wait 1s<br/>and Retry]
    WaitAndRetry --> NetworkError
    RetryNetwork -->|No| NetworkFallback[Use Fallback<br/>Network Path]
    
    DatabaseError --> CheckDBHealth{Database<br/>Healthy?}
    CheckDBHealth -->|No| SwitchToReplica[Switch to<br/>Read Replica]
    CheckDBHealth -->|Yes| RetryDB[Retry Database<br/>Operation]
    
    ServiceError --> RestartService{Service<br/>Restart Needed?}
    RestartService -->|Yes| GracefulRestart[Graceful Service<br/>Restart]
    RestartService -->|No| CircuitBreaker[Activate Circuit<br/>Breaker]
    
    ConsensusError --> CheckValidators{Validators<br/>Available?}
    CheckValidators -->|No| WaitForValidators[Wait for Validator<br/>Recovery]
    CheckValidators -->|Yes| RestartConsensus[Restart Consensus<br/>Round]
    
    NetworkFallback --> LogError[Log Error<br/>Details]
    SwitchToReplica --> LogError
    RetryDB --> LogError
    GracefulRestart --> LogError
    CircuitBreaker --> LogError
    WaitForValidators --> LogError
    RestartConsensus --> LogError
    
    LogError --> UpdateMetrics[Update Error<br/>Metrics]
    UpdateMetrics --> NotifyOps[Notify Operations<br/>Team]
    NotifyOps --> End([End])
```

### Load Balancing Strategy Flow

```mermaid
flowchart TD
    RequestReceived([Request Received]) --> CheckServiceHealth{Target Service<br/>Healthy?}
    CheckServiceHealth -->|No| FindHealthyInstance[Find Healthy<br/>Instance]
    CheckServiceHealth -->|Yes| CheckLoad{Instance Load<br/>Acceptable?}
    
    FindHealthyInstance --> HealthyFound{Healthy Instance<br/>Found?}
    HealthyFound -->|No| ReturnError[Return 503<br/>Service Unavailable]
    HealthyFound -->|Yes| RouteToHealthy[Route to Healthy<br/>Instance]
    
    CheckLoad -->|No| FindLowLoadInstance[Find Low Load<br/>Instance]
    CheckLoad -->|Yes| RouteToInstance[Route to<br/>Instance]
    
    FindLowLoadInstance --> LowLoadFound{Low Load Instance<br/>Found?}
    LowLoadFound -->|No| UseRoundRobin[Use Round Robin<br/>Algorithm]
    LowLoadFound -->|Yes| RouteToLowLoad[Route to Low Load<br/>Instance]
    
    UseRoundRobin --> RouteToNext[Route to Next<br/>Instance]
    
    RouteToHealthy --> UpdateMetrics[Update Load<br/>Metrics]
    RouteToInstance --> UpdateMetrics
    RouteToLowLoad --> UpdateMetrics
    RouteToNext --> UpdateMetrics
    ReturnError --> UpdateMetrics
    
         UpdateMetrics --> End([End])
```

## 🔗 Service Interaction Patterns

### Service Dependencies Matrix

| Service | Dependencies | Provides To | Port | Health Check |
|---------|-------------|-------------|------|--------------|
| **API Gateway** | Auth Service, API Service | External Clients | 8000 | `/health` |
| **Auth Service** | Redis Cache | API Gateway, All Services | 8088 | `/health` |
| **API Service** | Core Services | API Gateway | 8089 | `/health` |
| **Consensus Service** | State DB, Message Router | VM Manager, Chain Manager | 8080 | `/health` |
| **VM Manager** | State DB | Consensus Service, Chain Manager | 8081 | `/health` |
| **Chain Manager** | Block DB, Consensus Service | API Service, Indexer | 8082 | `/health` |
| **Validator Service** | Consensus Service, P2P Network | Consensus Service | 8083 | `/health` |
| **P2P Network** | Message Router, Peer Manager | Validator Service, Network | 8084 | `/health` |
| **Message Router** | Redis Queue, Peer Manager | P2P Network, Consensus | 8085 | `/health` |
| **Peer Manager** | Redis Cache | Message Router, P2P Network | 8086 | `/health` |
| **Indexer Service** | State DB, Block DB | External Queries | 8087 | `/health` |
| **Health Service** | All Core Services | Monitoring Systems | 8090 | `/health` |
| **Metrics Service** | Prometheus | Monitoring Dashboard | 8091 | `/health` |
| **Config Service** | File System | All Services | 8092 | `/health` |

### Data Flow Patterns

#### 1. **Request-Response Pattern**
```
Client → API Gateway → Auth Service → API Service → Core Service → Response
```
- **Use Case**: Standard API calls, queries
- **Latency**: Low (< 100ms)
- **Reliability**: High with circuit breakers

#### 2. **Event-Driven Pattern**
```
Service A → Message Router → Redis Queue → Service B → Event Processing
```
- **Use Case**: Asynchronous processing, notifications
- **Latency**: Medium (100-500ms)
- **Reliability**: High with message persistence

#### 3. **Consensus Pattern**
```
Proposer → Validators → Vote Collection → Consensus Decision → State Update
```
- **Use Case**: Block validation, state changes
- **Latency**: High (1-5s depending on network)
- **Reliability**: Very High with Byzantine fault tolerance

#### 4. **Streaming Pattern**
```
P2P Network → Message Router → Real-time Processing → Live Updates
```
- **Use Case**: Real-time data feeds, live monitoring
- **Latency**: Very Low (< 50ms)
- **Reliability**: Medium with reconnection logic

### Communication Protocols

| Protocol | Use Case | Services | Characteristics |
|----------|----------|----------|-----------------|
| **HTTP/REST** | API calls, health checks | All services | Synchronous, stateless |
| **gRPC** | Internal service communication | Core services | High performance, typed |
| **WebSocket** | Real-time updates | P2P Network, API Gateway | Bidirectional, persistent |
| **Redis Pub/Sub** | Event messaging | Message Router, Services | Asynchronous, scalable |
| **TCP** | P2P networking | P2P Network Service | Low-level, reliable |

### Security Patterns

#### Authentication Flow
```mermaid
sequenceDiagram
    participant Client
    participant APIGateway
    participant AuthService
    participant Redis
    
    Client->>APIGateway: Request + Credentials
    APIGateway->>AuthService: Validate Credentials
    AuthService->>Redis: Check User Session
    Redis-->>AuthService: Session Data
    AuthService-->>APIGateway: JWT Token
    APIGateway-->>Client: Authenticated Response
```

#### Authorization Flow
```mermaid
sequenceDiagram
    participant Client
    participant APIGateway
    participant AuthService
    participant CoreService
    
    Client->>APIGateway: Request + JWT
    APIGateway->>AuthService: Validate JWT
    AuthService-->>APIGateway: User Claims
    APIGateway->>APIGateway: Check Permissions
    APIGateway->>CoreService: Authorized Request
    CoreService-->>APIGateway: Response
    APIGateway-->>Client: Final Response
```

## 📁 Project Structure

```
microservices/
├── README.md                          # This file
├── docker-compose.yml                 # Local development setup
├── k8s/                               # Kubernetes manifests
│   ├── namespaces.yaml               # Namespace definitions
│   ├── core/                         # Core blockchain services
│   │   ├── consensus-service.yaml
│   │   └── vm-manager-service.yaml
│   ├── network/                      # Network layer services
│   │   └── p2p-network-service.yaml
│   ├── storage/                      # Data persistence layer
│   │   └── state-database.yaml
│   ├── gateway/                      # API gateway and routing
│   │   └── api-gateway.yaml
│   └── monitoring/                   # Observability stack
│       └── prometheus.yaml
├── services/                         # Service implementations
│   ├── consensus/                    # Consensus service
│   │   ├── main.go
│   │   ├── Dockerfile
│   │   └── go.mod
│   └── api-gateway/                  # API Gateway service
│       ├── main.go
│       ├── Dockerfile
│       └── go.mod
└── scripts/                          # Deployment and utility scripts
    └── deploy.sh                     # Main deployment script
```

## 🚀 Quick Start

### Prerequisites

- **Kubernetes cluster** (v1.25+)
- **kubectl** configured to access your cluster
- **Docker** (for building images)
- **Helm** (optional, for advanced deployments)
- **Istio** (optional, for service mesh features)

### Local Development with Docker Compose

1. **Start the development environment:**
   ```bash
   docker-compose up -d
   ```

2. **Check service status:**
   ```bash
   docker-compose ps
   ```

3. **View logs:**
   ```bash
   docker-compose logs -f consensus-service
   ```

4. **Stop the environment:**
   ```bash
   docker-compose down
   ```

### Production Deployment on Kubernetes

1. **Clone and navigate to the project:**
   ```bash
   git clone <repository-url>
   cd microservices
   ```

2. **Make deployment script executable:**
   ```bash
   chmod +x scripts/deploy.sh
   ```

3. **Deploy all components:**
   ```bash
   ./scripts/deploy.sh deploy
   ```

4. **Verify deployment:**
   ```bash
   ./scripts/deploy.sh verify
   ```

5. **Get access information:**
   ```bash
   ./scripts/deploy.sh info
   ```

## 🔧 Configuration

### Environment Variables

Each service can be configured using environment variables:

#### Consensus Service
- `CONSENSUS_MODE` - Consensus algorithm (snowman/avalanche)
- `VALIDATOR_THRESHOLD` - Validator threshold (default: 0.67)
- `DB_HOST` - Database host
- `REDIS_URL` - Redis connection URL

#### API Gateway
- `JWT_SECRET` - JWT signing secret
- `CONSENSUS_SERVICE_URL` - Consensus service endpoint
- `VM_MANAGER_URL` - VM Manager service endpoint

### Kubernetes Configuration

Services are organized into namespaces:
- `avalanche-core` - Core blockchain services
- `avalanche-network` - Network layer services
- `avalanche-storage` - Data persistence services
- `avalanche-gateway` - API gateway and routing
- `avalanche-monitoring` - Observability stack

## 📊 Monitoring and Observability

### Prometheus Metrics

All services expose Prometheus metrics on `/metrics` endpoint:

- **Consensus Service Metrics:**
  - `consensus_blocks_processed_total` - Total blocks processed
  - `consensus_blocks_produced_total` - Total blocks produced
  - `consensus_block_processing_duration_seconds` - Block processing latency
  - `consensus_active_validators` - Number of active validators

- **API Gateway Metrics:**
  - `http_requests_total` - Total HTTP requests
  - `http_request_duration_seconds` - Request duration
  - `auth_failures_total` - Authentication failures
  - `rate_limit_hits_total` - Rate limit violations

### Health Checks

Each service provides health check endpoints:
- `/health` - Basic health status
- `/ready` - Readiness for traffic
- `/startup` - Startup completion status

### Accessing Monitoring

1. **Prometheus:**
   ```bash
   kubectl port-forward svc/prometheus 9090:9090 -n avalanche-monitoring
   ```
   Access at: http://localhost:9090

2. **Grafana (if deployed):**
   ```bash
   kubectl port-forward svc/grafana 3000:3000 -n avalanche-monitoring
   ```
   Access at: http://localhost:3000

## 🔐 Security

### Authentication
- JWT-based authentication for API access
- Service-to-service authentication via Istio mTLS
- RBAC policies for Kubernetes resources

### Network Security
- Network policies for inter-service communication
- Pod security standards enforcement
- Secrets management with Kubernetes secrets

### TLS/SSL
- TLS termination at API Gateway
- Internal service mesh encryption with Istio
- Certificate management with cert-manager (optional)

## 📈 Performance Optimization

### Horizontal Scaling
- Horizontal Pod Autoscaler (HPA) configured for all services
- Custom metrics-based scaling for consensus and VM services
- Load balancing with Kubernetes services

### Resource Management
- Resource requests and limits defined for all containers
- Pod disruption budgets for high availability
- Node affinity rules for optimal placement

### Caching Strategy
- Redis-based caching for frequently accessed data
- Application-level caching in services
- Database query optimization

## 🛠️ Development

### Building Services

1. **Build individual service:**
   ```bash
   cd services/consensus
   docker build -t avalanche/consensus-service:latest .
   ```

2. **Build all services:**
   ```bash
   ./scripts/deploy.sh build
   ```

### Testing

1. **Unit tests:**
   ```bash
   cd services/consensus
   go test ./...
   ```

2. **Integration tests:**
   ```bash
   docker-compose -f docker-compose.test.yml up --abort-on-container-exit
   ```

### Adding New Services

1. Create service directory under `services/`
2. Implement service with health checks and metrics
3. Create Dockerfile and Kubernetes manifests
4. Update deployment scripts and documentation

## 🔄 CI/CD Pipeline

### GitHub Actions Workflow

The project includes a complete CI/CD pipeline:

1. **Build Stage:**
   - Code quality checks
   - Unit tests
   - Docker image builds

2. **Test Stage:**
   - Integration tests
   - Security scans
   - Performance tests

3. **Deploy Stage:**
   - Staging deployment
   - Smoke tests
   - Production deployment

### GitOps with ArgoCD

For production environments, use ArgoCD for GitOps deployment:

1. **Install ArgoCD:**
   ```bash
   kubectl create namespace argocd
   kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
   ```

2. **Configure application:**
   ```bash
   kubectl apply -f k8s/argocd/application.yaml
   ```

## 🚨 Troubleshooting

### Common Issues

1. **Pods not starting:**
   ```bash
   kubectl describe pod <pod-name> -n <namespace>
   kubectl logs <pod-name> -n <namespace>
   ```

2. **Service connectivity issues:**
   ```bash
   kubectl get svc -A
   kubectl get endpoints -A
   ```

3. **Database connection problems:**
   ```bash
   kubectl exec -it <postgres-pod> -n avalanche-storage -- psql -U postgres -d avalanche_state
   ```

### Performance Issues

1. **Check resource usage:**
   ```bash
   kubectl top pods -A
   kubectl top nodes
   ```

2. **Monitor metrics:**
   ```bash
   kubectl port-forward svc/prometheus 9090:9090 -n avalanche-monitoring
   ```

3. **Check HPA status:**
   ```bash
   kubectl get hpa -A
   ```

## 📚 API Documentation

### Consensus Service API

- `GET /status` - Get consensus status
- `POST /block` - Submit block for consensus
- `GET /validators` - List active validators
- `POST /validators` - Add new validator

### API Gateway Routes

- `POST /api/v1/auth/login` - Authenticate user
- `GET /api/v1/consensus/status` - Consensus status
- `POST /api/v1/consensus/block` - Submit block
- `GET /api/v1/vm/instances` - List VM instances

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests and documentation
5. Submit a pull request

### Development Guidelines

- Follow Go best practices
- Include comprehensive tests
- Update documentation
- Use semantic versioning
- Follow conventional commits

## 📄 License

This project is licensed under the BSD 3-Clause License - see the [LICENSE](../LICENSE) file for details.

## 🆘 Support

- **Documentation:** Check this README and inline code comments
- **Issues:** Create GitHub issues for bugs and feature requests
- **Discussions:** Use GitHub Discussions for questions and ideas
- **Community:** Join the Avalanche developer community

## 🔮 Roadmap

### Phase 1 (Current)
- ✅ Core microservices implementation
- ✅ Kubernetes deployment manifests
- ✅ Basic monitoring and observability
- ✅ API Gateway with authentication

### Phase 2 (Next)
- 🔄 Advanced consensus algorithms
- 🔄 Enhanced VM management
- 🔄 Comprehensive test suite
- 🔄 Performance benchmarking

### Phase 3 (Future)
- 📋 Multi-cloud deployment
- 📋 Advanced security features
- 📋 Machine learning optimizations
- 📋 Cross-chain interoperability

---

**Note:** This microservices implementation provides significant improvements in scalability, maintainability, and performance compared to the monolithic approach. The architecture enables independent scaling of components and supports much higher transaction throughput while maintaining the security and reliability of the Avalanche network. 