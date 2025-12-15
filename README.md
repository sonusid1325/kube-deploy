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
- **Configurable policies** — failure thresholds, retry limits, cooldown periods, analysis duration
- **Test application (`goserver`)** — Go HTTP server with configurable failure modes for E2E testing

## Project Structure

```
kube-deploy/
├── cmd/
│   ├── kube-deploy-server/    # gRPC server entrypoint
│   └── kdctl/                 # CLI client
├── proto/                     # Protobuf definitions
├── api/v1/                    # Generated gRPC Go code
├── pkg/
│   ├── models/                # Core domain types
│   ├── deployer/              # Deployment strategies (rolling, canary)
│   ├── health/                # Health monitoring engine + checkers
│   ├── rollback/              # Automated rollback controller
│   ├── k8s/                   # Kubernetes client wrapper
│   └── server/                # gRPC server implementation
├── internal/
│   └── config/                # Configuration loader (YAML + env vars)
├── deploy/
│   └── goserver/              # Test app: Dockerfile, K8s manifests
├── docs/                      # Roadmap and documentation
└── Makefile                   # Build, test, proto, lint targets
```

## Quick Start

### Prerequisites

- Go 1.23+
- A Kubernetes cluster (minikube, kind, or remote)
- `kubectl` configured with cluster access
- `protoc` + Go plugins (only needed to regenerate proto stubs)

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
./bin/kube-deploy-server --port 9090 --log-format console --log-level debug

# Or with in-cluster config
./bin/kube-deploy-server --in-cluster --port 9090
```

### Deploy the Test Application (goserver)

```bash
# Build Docker images
make docker-goserver-v1    # healthy baseline
make docker-goserver-v2    # healthy new version
make docker-goserver-v3-bad # unhealthy (for rollback testing)

# Deploy to your cluster
make deploy-goserver
```

### Use the CLI

```bash
# Rolling update
kdctl deploy -n default -d goserver --image goserver:v2 --strategy rolling

# Canary deployment
kdctl deploy -n default -d goserver --image goserver:v2 --strategy canary \
  --canary-replicas 1 --analysis-duration 60s --success-threshold 3

# Watch deployment status
kdctl status -n default -d goserver --watch

# Stream health events
kdctl health -n default -d goserver --watch --interval 5s

# Manual rollback
kdctl rollback -n default -d goserver --revision 3 --reason "broken endpoint"

# View revision history
kdctl history -n default -d goserver --limit 10

# Dry run (preview without applying)
kdctl deploy -n default -d goserver --image goserver:v2 --dry-run
```

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

## Health Monitoring

The health monitor continuously runs pluggable checks against deployments:

| Check Type | What It Does |
|---|---|
| **Pod Readiness** | Verifies all pods are in Ready condition via Kubernetes API |
| **Restart Count** | Detects CrashLoopBackOff and restart count spikes |
| **HTTP Probe** | Sends GET requests to a configurable endpoint on each pod |

Health events are streamed to clients via gRPC and trigger automated rollback when the failure threshold is exceeded.

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

## Configuration

The server reads configuration from YAML, environment variables, and CLI flags (in increasing precedence order).

### Environment Variables

| Variable | Description |
|---|---|
| `KD_SERVER_HOST` | gRPC listen host |
| `KD_SERVER_PORT` | gRPC listen port |
| `KD_KUBECONFIG` | Path to kubeconfig file |
| `KD_KUBE_CONTEXT` | Kubernetes context to use |
| `KD_IN_CLUSTER` | Use in-cluster config (`true`/`false`) |
| `KD_DEPLOY_STRATEGY` | Default deploy strategy |
| `KD_ROLLOUT_TIMEOUT` | Rollout timeout (e.g., `5m`) |
| `KD_ROLLBACK_ENABLED` | Enable auto-rollback (`true`/`false`) |
| `KD_LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) |
| `KD_LOG_FORMAT` | Log format (`json`, `console`) |

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

## Development

```bash
# Install development tools
make install-tools

# Format, vet, build, and test
make all

# Generate protobuf stubs (requires protoc)
make proto

# Run tests with race detector
make test-race

# Generate coverage report
make cover

# Run the linter
make lint

# See all available targets
make help
```

## Tech Stack

| Component | Technology |
|---|---|
| Language | Go 1.23+ |
| Kubernetes client | client-go v0.31.x |
| RPC framework | gRPC + Protocol Buffers |
| CLI framework | Cobra |
| Configuration | YAML + envconfig |
| Logging | Zap (structured JSON/console) |
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