# kube-deploy — Zero-Downtime Kubernetes Deployment Pipeline

## Project Roadmap

A production-grade, zero-downtime deployment pipeline for Kubernetes clusters with automated rollback, health monitoring, and gRPC-based control plane. Written in Go.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                    kdctl (CLI Client)                    │
│              gRPC client / human interface               │
└──────────────────────┬──────────────────────────────────┘
                       │ gRPC
┌──────────────────────▼──────────────────────────────────┐
│               kube-deploy-server (gRPC)                  │
│                                                          │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │  Deployer    │  │   Health     │  │   Rollback     │  │
│  │  Engine      │  │   Monitor    │  │   Controller   │  │
│  │             │  │              │  │                │  │
│  │ - Rolling   │  │ - Readiness  │  │ - Auto-revert  │  │
│  │ - Canary    │  │ - Liveness   │  │ - Revision     │  │
│  │ - Blue/Green│  │ - Custom     │  │   history      │  │
│  └──────┬──────┘  └──────┬───────┘  └───────┬────────┘  │
│         │               │                  │            │
│  ┌──────▼───────────────▼──────────────────▼────────┐   │
│  │              Kubernetes Client (pkg/k8s)          │   │
│  │         Typed client for Deployments, Services,   │   │
│  │         Pods, ReplicaSets, Events                 │   │
│  └──────────────────────┬───────────────────────────┘   │
└─────────────────────────┼───────────────────────────────┘
                          │
              ┌───────────▼───────────┐
              │   Kubernetes Cluster   │
              │                       │
              │  ┌─────────────────┐  │
              │  │    goserver     │  │
              │  │  (test target)  │  │
              │  └─────────────────┘  │
              └───────────────────────┘
```

---

## Phase 1: Project Scaffolding & Core Types

**Goal:** Establish project structure, Go modules, core domain models, and configuration.

### Deliverables
- [x] Go module initialization (`go.mod`)
- [x] Domain models: `DeploymentRequest`, `DeploymentStatus`, `HealthCheckResult`, `RollbackPolicy`
- [x] Configuration loader (`internal/config`)
- [x] Makefile with build/test/proto/lint targets

### Directory Layout
```
kube-deploy/
├── cmd/
│   ├── kube-deploy-server/    # gRPC server entrypoint
│   └── kdctl/                 # CLI client
├── proto/                     # Protobuf definitions
├── api/v1/                    # Generated gRPC Go code
├── pkg/
│   ├── models/                # Core domain types
│   ├── deployer/              # Deployment strategies
│   ├── health/                # Health monitoring
│   ├── rollback/              # Automated rollback
│   ├── k8s/                   # Kubernetes client wrapper
│   └── server/                # gRPC server implementation
├── internal/
│   └── config/                # Configuration
├── deploy/
│   └── goserver/              # Test app manifests
└── docs/                      # Documentation
```

---

## Phase 2: gRPC API Definition

**Goal:** Define the complete gRPC service contract for deployments, health, and rollback.

### Deliverables
- [x] `proto/kube_deploy.proto` — full service definition
- [x] Generated Go stubs in `api/v1/`
- [x] Services: `DeploymentService`, streaming status updates

### RPC Methods
| Method | Type | Description |
|---|---|---|
| `Deploy` | Server-stream | Initiate a deployment, stream progress |
| `GetDeploymentStatus` | Unary | Query current deployment state |
| `ListDeployments` | Unary | List all tracked deployments |
| `Rollback` | Unary | Trigger a manual rollback |
| `WatchHealth` | Server-stream | Stream health check results in real time |
| `GetHistory` | Unary | Retrieve deployment revision history |

---

## Phase 3: Kubernetes Deployer Engine

**Goal:** Implement the core deployment engine with zero-downtime strategies.

### Deliverables
- [x] `pkg/k8s/client.go` — Kubernetes client wrapper (typed client-go)
- [x] `pkg/deployer/deployer.go` — Strategy interface + orchestrator
- [x] `pkg/deployer/rolling.go` — Rolling update strategy
- [x] `pkg/deployer/canary.go` — Canary deployment strategy

### Deployment Strategies

#### Rolling Update
1. Patch Deployment with new image tag
2. Set `maxUnavailable: 0`, `maxSurge: 1` for zero downtime
3. Watch rollout status via ReplicaSet convergence
4. Emit progress events on gRPC stream

#### Canary
1. Create a canary Deployment (e.g., 1 replica with new image)
2. Route a percentage of traffic via label selectors / service mesh
3. Run health checks against canary pods
4. If healthy → scale up canary, scale down old; if unhealthy → kill canary
5. Configurable: `canaryPercent`, `analysisDuration`, `successThreshold`

---

## Phase 4: Health Monitor & Automated Rollback

**Goal:** Continuously monitor deployed pods and auto-rollback on failure.

### Deliverables
- [x] `pkg/health/monitor.go` — Health monitoring engine
- [x] `pkg/health/checks.go` — Pluggable health check implementations
- [x] `pkg/rollback/rollback.go` — Rollback controller

### Health Checks
| Check | Source | Trigger |
|---|---|---|
| Pod readiness | Kubernetes API | Pod not Ready within timeout |
| Restart count | Kubernetes API | CrashLoopBackOff / restart spike |
| HTTP probe | Direct HTTP call | Non-2xx from app health endpoint |
| Custom metric | Prometheus (future) | Error rate threshold exceeded |

### Rollback Logic
1. Detect failure via health monitor (configurable thresholds)
2. Look up last-known-good revision from Deployment's revision history
3. Execute `kubectl rollout undo` equivalent via client-go
4. Emit rollback event on gRPC stream
5. Re-run health checks to verify rollback succeeded

### Rollback Policy (Configurable)
```yaml
rollbackPolicy:
  enabled: true
  maxRetries: 2
  healthCheckInterval: 10s
  healthCheckTimeout: 120s
  failureThreshold: 3
  successThreshold: 2
```

---

## Phase 5: gRPC Server & CLI Client

**Goal:** Wire everything together into a running server and a usable CLI.

### Deliverables
- [x] `pkg/server/server.go` — gRPC server with all service handlers
- [x] `cmd/kube-deploy-server/main.go` — Server entrypoint
- [x] `cmd/kdctl/main.go` — CLI client for interacting with the server

### CLI Commands
```bash
# Deploy a new version
kdctl deploy --namespace default --deployment goserver --image goserver:v2 --strategy rolling

# Watch deployment progress
kdctl status --namespace default --deployment goserver --watch

# Check health
kdctl health --namespace default --deployment goserver --watch

# Manual rollback
kdctl rollback --namespace default --deployment goserver --revision 3

# List deployment history
kdctl history --namespace default --deployment goserver
```

---

## Phase 6: Testing with goserver

**Goal:** End-to-end validation using a real Go HTTP server as the deployment target.

### Test Application: goserver
- Simple Go HTTP server with `/healthz` and `/readyz` endpoints
- Configurable failure modes for testing rollback (env var `FAIL_AFTER`)
- Dockerfile + Kubernetes manifests in `deploy/goserver/`

### Test Scenarios

| # | Scenario | Expected Outcome |
|---|---|---|
| 1 | Rolling update v1 → v2 (healthy) | Zero-downtime upgrade, all pods healthy |
| 2 | Rolling update v1 → v3 (crashloop) | Auto-rollback to v1, alert emitted |
| 3 | Canary deploy v1 → v2 (10% traffic) | Canary pod created, promoted after health pass |
| 4 | Canary deploy v1 → v3 (unhealthy) | Canary killed, v1 unchanged |
| 5 | Manual rollback to specific revision | Deployment reverted to exact revision |
| 6 | Health monitor detects restart spike | Rollback triggered within threshold window |

### Test Manifests
- `deploy/goserver/deployment.yaml` — Base Deployment
- `deploy/goserver/service.yaml` — ClusterIP Service
- `deploy/goserver/Dockerfile` — Multi-stage Go build

---

## Tech Stack

| Component | Technology |
|---|---|
| Language | Go 1.23+ |
| Kubernetes client | client-go v0.31.x |
| RPC framework | gRPC + Protocol Buffers |
| CLI framework | cobra |
| Config | YAML + envconfig |
| Testing | Go testing + testify |
| Container runtime | Docker / containerd |
| CI/CD (future) | GitHub Actions |

---

## Future Enhancements (Post-MVP)

- [ ] **Blue/Green strategy** — full environment swap
- [ ] **Prometheus integration** — metric-based canary analysis
- [ ] **Webhook notifications** — Slack, PagerDuty on rollback events
- [ ] **Multi-cluster support** — deploy across federated clusters
- [ ] **GitOps mode** — watch a Git repo for manifest changes
- [ ] **Web dashboard** — real-time deployment visualization
- [ ] **RBAC** — gRPC auth with Kubernetes ServiceAccount tokens
- [ ] **Helm/Kustomize** — support templated manifests as input

---

## Getting Started

```bash
# Build everything
make build

# Generate protobuf stubs
make proto

# Run the server (uses ~/.kube/config by default)
./bin/kube-deploy-server --port 9090

# Deploy goserver v1
kdctl deploy --namespace default --deployment goserver --image goserver:v1 --strategy rolling

# Watch it roll out
kdctl status --namespace default --deployment goserver --watch
```

---

## Status

| Phase | Status |
|---|---|
| Phase 1: Scaffolding | ✅ Complete |
| Phase 2: gRPC API | ✅ Complete |
| Phase 3: Deployer Engine | ✅ Complete |
| Phase 4: Health & Rollback | ✅ Complete |
| Phase 5: Server & CLI | ✅ Complete |
| Phase 6: goserver Testing | 🔜 Ready for validation |