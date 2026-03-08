# kube-deploy

**Zero-downtime deployment pipeline for Kubernetes clusters with automated rollback and health monitoring.**

Built with Go, Kubernetes client-go, and gRPC.

---

## Architecture

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
│  └──────────────────────┬───────────────────────────┘   │
└─────────────────────────┼───────────────────────────────┘
                          │
              ┌───────────▼───────────┐
              │   Kubernetes Cluster   │
              │   (goserver test app)  │
              └───────────────────────┘
```

## Features

- **Zero-downtime rolling updates** — `maxUnavailable: 0`, `maxSurge: 1` by default
- **Canary deployments** — spin up a canary, run health analysis, promote or abort
- **Automated rollback** — health monitor detects failures and reverts to last-known-good revision
- **Real-time streaming** — gRPC server-streaming for deployment progress and health events
- **Pluggable health checks** — pod readiness, restart count, HTTP probes, custom metrics
- **CLI client (`kdctl`)** — deploy, status, health, rollback, and history commands
- **Full K8s deployment stack** — Namespace, ServiceAccount, RBAC, ConfigMap, Deployment, Service
- **Kustomize support** — `kubectl apply -k deploy/k8s/` for one-command deployment
- **One-command deploy script** — `deploy.sh` with `--dry-run`, `--delete`, `--server-only`, `--goserver-only`
- **Configurable policies** — failure thresholds, retry limits, cooldown periods, analysis duration
- **Test application (`goserver`)** — Go HTTP server with configurable failure modes for E2E testing

## Project Structure

```
kube-deploy/
├── cmd/
│   ├── kube-deploy-server/        # gRPC server entrypoint
│   └── kdctl/                     # CLI client
├── proto/                         # Protobuf definitions
├── api/v1/                        # Generated gRPC Go code
├── pkg/
│   ├── models/                    # Core domain types
│   ├── deployer/                  # Deployment strategies (rolling, canary)
│   ├── health/                    # Health monitoring engine + checkers
│   ├── rollback/                  # Automated rollback controller
│   ├── k8s/                       # Kubernetes client wrapper
│   └── server/                    # gRPC server implementation
├── internal/
│   └── config/                    # Configuration loader (YAML + env vars)
├── deploy/
│   ├── k8s/                       # kube-deploy server K8s manifests
│   │   ├── namespace.yaml         #   └─ Namespace
│   │   ├── serviceaccount.yaml    #   └─ ServiceAccount
│   │   ├── rbac.yaml              #   └─ ClusterRole + ClusterRoleBinding
│   │   ├── configmap.yaml         #   └─ Server configuration
│   │   ├── deployment.yaml        #   └─ Server Deployment (2 replicas)
│   │   ├── service.yaml           #   └─ ClusterIP Service (gRPC :9090)
│   │   └── kustomization.yaml     #   └─ Kustomize orchestration
│   ├── goserver/                  # Test app: Dockerfile, K8s manifests
│   └── deploy.sh                  # One-command deploy/teardown script
├── Dockerfile                     # Multi-stage build for kube-deploy-server
├── docs/                          # Roadmap and documentation
└── Makefile                       # Build, test, proto, deploy targets
```

## Quick Start

### Prerequisites

- Go 1.23+
- A Kubernetes cluster (minikube, kind, or remote)
- `kubectl` configured with cluster access
- `protoc` + Go plugins (only needed to regenerate proto stubs)
- Docker (for building container images)

### Build

```bash
# Build both server and CLI
make build

# Outputs:
#   bin/kube-deploy-server
#   bin/kdctl
```

### Run the Server

```bash
# Uses ~/.kube/config by default
./bin/kube-deploy-server --port 9090

# With debug logging
./bin/kube-deploy-server --port 9090 --log-format console --log-level debug

# With in-cluster config (when running inside a K8s pod)
./bin/kube-deploy-server --in-cluster --port 9090
```

> **Note:** `--in-cluster` only works when running inside a Kubernetes pod with a mounted ServiceAccount token. For local development, omit it.

### Use the CLI

```bash
# Check deployment status
./bin/kdctl status -n default -d goserver --server localhost:9090

# Check health
./bin/kdctl health -n default -d goserver --server localhost:9090

# View revision history
./bin/kdctl history -n default -d goserver --server localhost:9090

# Rolling update
./bin/kdctl deploy -n default -d goserver --image goserver:v2 --strategy rolling --server localhost:9090

# Canary deployment
./bin/kdctl deploy -n default -d goserver --image goserver:v2 --strategy canary --server localhost:9090

# Dry run (preview without applying)
./bin/kdctl deploy -n default -d goserver --image goserver:v2 --dry-run --server localhost:9090

# Watch deployment status (streaming)
./bin/kdctl status -n default -d goserver --watch --server localhost:9090

# Watch health events (streaming)
./bin/kdctl health -n default -d goserver --watch --server localhost:9090

# Manual rollback to a specific revision
./bin/kdctl rollback -n default -d goserver --revision 1 --server localhost:9090
```

### Example CLI Output

**Status:**
```
╭─ Deployment Status: default/goserver
╰─────────────────────────────────────
  Phase:           ✅ COMPLETED
  Image:           goserver-nmap:latest
  Replicas:        3/3 ready, 3 updated, 3 available
  Revision:        2
  Health:          ✅ HEALTHY
  Last Updated:    2026-03-08 16:09:20

  Conditions:
    • Available=True: Deployment has minimum availability.
    • Progressing=True: ReplicaSet "goserver-6454b855b5" has successfully progressed.

  Pods:
    NAME                                          STATUS       READY   RESTARTS   IMAGE
    goserver-6454b855b5-6qtw9                     Running      ✓       0          goserver-nmap:latest
    goserver-6454b855b5-rsh8r                     Running      ✓       0          goserver-nmap:latest
    goserver-6454b855b5-xhpcc                     Running      ✓       0          goserver-nmap:latest
```

**Health:**
```
╭─ Health: default/goserver
╰──────────────────────────
  Overall: ✅ HEALTHY
  Ready:   3/3

  Pods:
    ✓ goserver-6454b855b5-6qtw9                restarts=0    goserver-nmap:latest
    ✓ goserver-6454b855b5-rsh8r                restarts=0    goserver-nmap:latest
    ✓ goserver-6454b855b5-xhpcc                restarts=0    goserver-nmap:latest
```

**History:**
```
╭─ Deployment History: default/goserver
╰──────────────────────────────────────

  REVISION   IMAGE                   REPLICAS   DEPLOYED AT          NOTES
  --------   -----                   --------   -----------          -----
  1          goserver:latest         0          2026-03-08 14:39:02
  2          goserver-nmap:latest    3          2026-03-08 14:41:18
```

---

## Docker Images

### kube-deploy-server

```bash
# Build the server image
make docker-server

# Or manually with version info
docker build -t kube-deploy-server:latest \
  --build-arg VERSION=v1.0.0 \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -f Dockerfile .
```

### goserver (test application)

```bash
make docker-goserver-v1      # healthy baseline
make docker-goserver-v2      # healthy new version
make docker-goserver-v3-bad  # unhealthy (for rollback testing)
```

---

## Deploying to Kubernetes

### Option 1: Kustomize (recommended)

```bash
# Preview all resources
kubectl kustomize deploy/k8s/

# Apply everything in the correct order
kubectl apply -k deploy/k8s/

# Tear down
kubectl delete -k deploy/k8s/
```

### Option 2: deploy.sh script

```bash
# Deploy everything (server + goserver)
./deploy/deploy.sh

# Deploy only the kube-deploy server stack
./deploy/deploy.sh --server-only

# Deploy only the goserver test app
./deploy/deploy.sh --goserver-only

# Preview without applying
./deploy/deploy.sh --dry-run

# Tear down everything
./deploy/deploy.sh --delete

# Custom rollout timeout
./deploy/deploy.sh --timeout 180s
```

### Option 3: Makefile targets

```bash
# Deploy everything
make deploy-all

# Deploy only the server stack (namespace, SA, RBAC, configmap, service, deployment)
make deploy-server

# Deploy only goserver
make deploy-goserver

# Remove everything
make undeploy-all

# Remove only the server
make undeploy-server

# Remove only goserver
make undeploy-goserver
```

### Option 4: Manual kubectl

```bash
# Server stack (apply in order)
kubectl apply -f deploy/k8s/namespace.yaml
kubectl apply -f deploy/k8s/serviceaccount.yaml
kubectl apply -f deploy/k8s/rbac.yaml
kubectl apply -f deploy/k8s/configmap.yaml
kubectl apply -f deploy/k8s/service.yaml
kubectl apply -f deploy/k8s/deployment.yaml

# goserver test app
kubectl apply -f deploy/goserver/deployment.yaml
kubectl apply -f deploy/goserver/service.yaml
```

### What gets deployed

| Resource | Namespace | Description |
|---|---|---|
| Namespace `kube-deploy` | — | Dedicated namespace for the control plane |
| ServiceAccount `kube-deploy-server` | `kube-deploy` | Pod identity for K8s API access |
| ClusterRole `kube-deploy-server` | — | Permissions for Deployments, ReplicaSets, Pods, Services, Events |
| ClusterRoleBinding `kube-deploy-server` | — | Binds the ClusterRole to the ServiceAccount |
| ConfigMap `kube-deploy-config` | `kube-deploy` | Server config (strategies, health, rollback defaults) |
| Deployment `kube-deploy-server` | `kube-deploy` | 2-replica gRPC server with probes and security context |
| Service `kube-deploy-server` | `kube-deploy` | ClusterIP on port 9090 |

### Connecting to the server in-cluster

```bash
# Port-forward for local CLI access
kubectl port-forward -n kube-deploy svc/kube-deploy-server 9090:9090

# Then use kdctl
./bin/kdctl status -n default -d goserver --server localhost:9090
```

---

## Deployment Strategies

### Rolling Update

The default strategy. Updates the Deployment image in-place with zero-downtime guarantees:

1. Patches the Deployment with the new image, `maxUnavailable=0`, `maxSurge=1`
2. Polls rollout status until all replicas are updated and ready
3. Streams progress events to the client in real time
4. On timeout or failure, emits a `FAILED` event (triggering auto-rollback if enabled)

### Canary

Creates a separate canary Deployment alongside the stable one:

1. Labels the existing deployment as `stable`
2. Creates a canary Deployment with the new image (configurable replica count)
3. Both stable and canary pods receive traffic through the shared Service
4. Runs health analysis against canary pods for a configurable duration
5. If healthy → promotes (updates stable image, deletes canary)
6. If unhealthy → aborts (deletes canary, stable is untouched)

---

## Health Monitoring

The health monitor continuously runs pluggable checks against deployments:

| Check Type | What It Does |
|---|---|
| **Pod Readiness** | Verifies all pods are in Ready condition via Kubernetes API |
| **Restart Count** | Detects CrashLoopBackOff and restart count spikes |
| **HTTP Probe** | Sends GET requests to a configurable endpoint on each pod |

Health events are streamed to clients via gRPC and trigger automated rollback when the failure threshold is exceeded.

---

## Automated Rollback

The rollback controller integrates with the health monitor:

1. Receives unhealthy notifications when failure threshold is crossed
2. Checks rollback policy (enabled, max retries, cooldown)
3. Finds the previous revision from ReplicaSet history
4. Patches the Deployment's pod template to match the target revision
5. Waits for rollback rollout to complete
6. Runs post-rollback health verification
7. Records the result and fires notification callbacks

### Rollback Policy

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

## Configuration

The server reads configuration from YAML, environment variables, and CLI flags (in increasing precedence order).

The default configuration is provided via ConfigMap at [`deploy/k8s/configmap.yaml`](deploy/k8s/configmap.yaml).

### Environment Variables

| Variable | Description | Default |
|---|---|---|
| `KD_SERVER_HOST` | gRPC listen host | `0.0.0.0` |
| `KD_SERVER_PORT` | gRPC listen port | `9090` |
| `KD_KUBECONFIG` | Path to kubeconfig file | `~/.kube/config` |
| `KD_KUBE_CONTEXT` | Kubernetes context to use | current context |
| `KD_IN_CLUSTER` | Use in-cluster config | `false` |
| `KD_DEPLOY_STRATEGY` | Default deploy strategy | `rolling` |
| `KD_ROLLOUT_TIMEOUT` | Rollout timeout | `5m` |
| `KD_ROLLBACK_ENABLED` | Enable auto-rollback | `true` |
| `KD_LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |
| `KD_LOG_FORMAT` | Log format (`json`, `console`) | `json` |

---

## gRPC API

| Method | Type | Description |
|---|---|---|
| `Deploy` | Server-stream | Initiate a deployment, stream progress |
| `GetDeploymentStatus` | Unary | Query current deployment state |
| `ListDeployments` | Unary | List all tracked deployments |
| `Rollback` | Unary | Trigger a manual rollback |
| `WatchHealth` | Server-stream | Stream health check results in real time |
| `GetHistory` | Unary | Retrieve deployment revision history |

See [`proto/kube_deploy.proto`](proto/kube_deploy.proto) for the full service definition.

---

## Testing with goserver

The `goserver` test application is a simple Go HTTP server designed for testing kube-deploy:

- `/healthz` — liveness endpoint (returns 200 or 503)
- `/readyz` — readiness endpoint (returns 200 or 503)
- `/info` — JSON server info (version, uptime, request count)
- `/admin/unhealthy` — toggle unhealthy state at runtime
- `/admin/not-ready` — toggle not-ready state at runtime

### Configurable Failure Modes

| Env Var | Description |
|---|---|
| `VERSION` | Server version string (used to distinguish deployments) |
| `FAIL_AFTER` | Duration after which to simulate failure (e.g., `30s`) |
| `FAIL_MODE` | `unhealthy` (503s) or `crash` (exit 1 for CrashLoopBackOff) |

### Test Scenarios

| # | Scenario | Expected Outcome |
|---|---|---|
| 1 | Rolling update v1 → v2 (healthy) | Zero-downtime upgrade, all pods healthy |
| 2 | Rolling update v1 → v3-bad (crashloop) | Auto-rollback to v1, alert emitted |
| 3 | Canary deploy v1 → v2 (10% traffic) | Canary pod created, promoted after health pass |
| 4 | Canary deploy v1 → v3-bad (unhealthy) | Canary killed, v1 unchanged |
| 5 | Manual rollback to specific revision | Deployment reverted to exact revision |
| 6 | Health monitor detects restart spike | Rollback triggered within threshold window |

---

## Development

```bash
# Install development tools (protoc-gen-go, protoc-gen-go-grpc, golangci-lint)
make install-tools

# Format, vet, build, and test
make all

# Generate protobuf stubs (requires protoc)
make proto

# Run unit tests
make test

# Run tests with race detector
make test-race

# Run integration tests (requires a cluster)
make test-integration

# Generate coverage report
make cover

# Run the linter
make lint

# See all available targets
make help
```

### Makefile Targets

| Target | Description |
|---|---|
| `build` | Build server and CLI binaries |
| `build-server` | Build `kube-deploy-server` |
| `build-cli` | Build `kdctl` |
| `proto` | Generate Go code from protobuf |
| `test` | Run unit tests |
| `test-race` | Run tests with race detector |
| `test-integration` | Run integration tests |
| `cover` | Generate coverage report |
| `lint` | Run golangci-lint |
| `docker-server` | Build kube-deploy-server Docker image |
| `docker-goserver` | Build goserver Docker image |
| `docker-goserver-v1` | Build goserver:v1 (healthy) |
| `docker-goserver-v2` | Build goserver:v2 (healthy, new version) |
| `docker-goserver-v3-bad` | Build goserver:v3-bad (unhealthy) |
| `deploy-all` | Deploy server + goserver to cluster |
| `deploy-server` | Deploy kube-deploy server stack |
| `deploy-goserver` | Deploy goserver test app |
| `undeploy-all` | Remove everything from cluster |
| `undeploy-server` | Remove kube-deploy server stack |
| `undeploy-goserver` | Remove goserver test app |
| `run-server` | Build and run the server locally |
| `install-tools` | Install development tools |
| `clean` | Remove build artifacts |

---

## Tech Stack

| Component | Technology |
|---|---|
| Language | Go 1.23+ |
| Kubernetes client | client-go v0.31.x |
| RPC framework | gRPC + Protocol Buffers |
| CLI framework | Cobra |
| Configuration | YAML + envconfig |
| Logging | Zap (structured JSON/console) |
| Container runtime | Docker / containerd |
| Base image | `gcr.io/distroless/static-debian12:nonroot` |
| Orchestration | Kustomize |
| Testing | Go testing + testify |

## Roadmap

See [`docs/ROADMAP.md`](docs/ROADMAP.md) for the full project roadmap and future enhancements including:

- Blue/Green strategy
- Prometheus metric-based canary analysis
- Webhook notifications (Slack, PagerDuty)
- Multi-cluster support
- GitOps mode
- Web dashboard
- RBAC with Kubernetes ServiceAccount tokens
- Helm/Kustomize manifest support

## License

MIT