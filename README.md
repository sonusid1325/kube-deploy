# kdctl

**Zero-downtime Kubernetes deployment pipeline — a single binary with an interactive Bubble Tea TUI, CLI subcommands, built-in gRPC server, automated rollback, and health monitoring.**

Built with Go, Bubble Tea, Kubernetes client-go, and gRPC.

---

## Architecture

`kdctl` is a single binary that does everything:

```
┌──────────────────────────────────────────────────────────────────┐
│                         kdctl (single binary)                     │
│                                                                   │
│  ┌─────────────┐  ┌──────────────┐  ┌───────────────────────┐   │
│  │  Bubble Tea  │  │  CLI         │  │  gRPC Server          │   │
│  │  TUI (ui)    │  │  Subcommands │  │  (kdctl start)        │   │
│  └──────┬───────┘  └──────┬───────┘  └───────────┬───────────┘   │
│         │                 │                       │               │
│  ┌──────▼─────────────────▼───────────────────────▼────────────┐ │
│  │  ┌───────────┐  ┌────────────┐  ┌──────────────────┐       │ │
│  │  │ Deployer  │  │   Health   │  │     Rollback     │       │ │
│  │  │ Engine    │  │   Monitor  │  │     Controller   │       │ │
│  │  └─────┬─────┘  └──────┬─────┘  └────────┬─────────┘       │ │
│  │        │               │                  │                 │ │
│  │  ┌─────▼───────────────▼──────────────────▼──────────────┐  │ │
│  │  │              Kubernetes Client (pkg/k8s)               │  │ │
│  │  └──────────────────────┬─────────────────────────────────┘  │ │
│  └─────────────────────────┼────────────────────────────────────┘ │
└────────────────────────────┼─────────────────────────────────────┘
                             │
                 ┌───────────▼───────────┐
                 │   Kubernetes Cluster   │
                 └───────────────────────┘
```

All modes (TUI, CLI, server) talk directly to the Kubernetes API — no separate server process required for local usage.

---

## Features

- **Single binary** — one `kdctl` binary replaces three separate tools (TUI, server, CLI client)
- **Interactive TUI** — Bubble Tea terminal interface with tabs for status, health, deploy, rollback, history, and logs
- **CLI subcommands** — scriptable, non-interactive commands for CI/CD pipelines and automation
- **Built-in gRPC server** — `kdctl start` runs the server for remote or multi-user access
- **Real-time event streaming** — deploy events stream into the TUI live via `program.Send()`
- **Zero-downtime rolling updates** — `maxUnavailable: 0`, `maxSurge: 1` by default
- **Canary deployments** — spin up a canary, run health analysis, promote or abort
- **Automated rollback** — health monitor detects failures and reverts to last-known-good revision
- **Confirmation modals** — destructive actions (deploy, rollback) require explicit confirmation in the TUI
- **Help overlay** — press `?` for a full keyboard shortcut reference
- **Pluggable health checks** — pod readiness, restart count, HTTP probes, custom metrics
- **Full K8s deployment stack** — Namespace, ServiceAccount, RBAC, ConfigMap, Deployment, Service
- **Kustomize support** — `kubectl apply -k deploy/k8s/` for one-command cluster deployment
- **Configurable policies** — failure thresholds, retry limits, cooldown periods, analysis duration
- **Cross-compilation** — `make build-all-platforms` for Linux, macOS, and Windows
- **Test application (`goserver`)** — Go HTTP server with configurable failure modes for E2E testing

---

## Project Structure

```
kube-deploy/
├── cmd/
│   └── kdctl/                     # Unified entrypoint (TUI + CLI + server)
├── internal/
│   ├── tui/                       # Bubble Tea TUI implementation
│   │   ├── model.go               #   └─ Core model, Init/Update/View, form navigation
│   │   ├── views.go               #   └─ Tab rendering, help overlay, confirm modal
│   │   ├── commands.go            #   └─ Async K8s commands, live event streaming
│   │   ├── messages.go            #   └─ Bubble Tea message types
│   │   ├── types.go               #   └─ Data types, tab/field enums
│   │   ├── styles.go              #   └─ Lipgloss styles, color palette, badges
│   │   └── helpers.go             #   └─ Utility functions
│   └── config/                    # Configuration loader (YAML + env vars)
├── pkg/
│   ├── models/                    # Core domain types
│   ├── deployer/                  # Deployment strategies (rolling, canary)
│   ├── health/                    # Health monitoring engine + checkers
│   ├── rollback/                  # Automated rollback controller
│   ├── k8s/                       # Kubernetes client wrapper
│   └── server/                    # gRPC server implementation
├── proto/                         # Protobuf definitions
├── api/v1/                        # Generated gRPC Go code
├── deploy/
│   ├── k8s/                       # kube-deploy server K8s manifests
│   │   ├── namespace.yaml
│   │   ├── serviceaccount.yaml
│   │   ├── rbac.yaml
│   │   ├── configmap.yaml
│   │   ├── deployment.yaml
│   │   ├── service.yaml
│   │   └── kustomization.yaml
│   ├── goserver/                  # Test app: Dockerfile, K8s manifests
│   └── deploy.sh                  # One-command deploy/teardown script
├── Dockerfile                     # Multi-stage build for kdctl
├── docs/                          # Roadmap and documentation
└── Makefile                       # Build, test, proto, deploy targets
```

---

## Quick Start

### Prerequisites

- Go 1.23+
- A Kubernetes cluster (minikube, kind, or remote)
- `kubectl` configured with cluster access
- `protoc` + Go plugins (only needed to regenerate proto stubs)
- Docker (for building container images)

### Build

```bash
# Build kdctl
make build

# Output: bin/kdctl

# Install to $GOPATH/bin
make install

# Cross-compile for all platforms
make build-all-platforms
```

### Launch the Interactive TUI

```bash
# Target a deployment in the default namespace
kdctl -n default -d goserver

# Or use the explicit subcommand
kdctl ui -n default -d goserver

# Use a specific kubeconfig and context
kdctl -d myapp --kubeconfig ~/.kube/prod.yaml --context prod-cluster

# Use in-cluster config (when running inside a pod)
kdctl -d myapp --in-cluster
```

The TUI connects directly to your Kubernetes cluster — no server process required.

### CLI Subcommands (Non-Interactive)

```bash
# Check deployment status
kdctl status -n default -d goserver

# Check health
kdctl health -n default -d goserver

# Deploy a new image (rolling update)
kdctl deploy -n default -d goserver --image goserver:v2

# Canary deployment
kdctl deploy -n default -d goserver --image goserver:v2 --strategy canary

# Dry run (preview without applying)
kdctl deploy -n default -d goserver --image goserver:v2 --dry-run

# Rollback to previous revision
kdctl rollback -n default -d goserver

# Rollback to a specific revision
kdctl rollback -n default -d goserver --revision 3

# View revision history
kdctl history -n default -d goserver

# Watch status continuously
kdctl status -n default -d goserver --watch

# Watch health continuously
kdctl health -n default -d goserver --watch --interval 5s

# Start the gRPC server
kdctl start --port 9090 --log-level debug --log-format console

# Print version
kdctl version
```

---

## TUI Guide

### Tabs

| Tab | Key | Description |
|-----|-----|-------------|
| **Status** | `1` | Deployment overview, replica counts, pod table, conditions |
| **Health** | `2` | Overall health badge, progress bar, per-pod health icons |
| **Deploy** | `3` | Form to submit a new deployment (image, strategy, params) |
| **Rollback** | `4` | Form to rollback to a previous revision with reason |
| **History** | `5` | Revision history table with images, timestamps, and notes |
| **Logs** | `6` | Timestamped activity log of all TUI actions and events |

### Keyboard Shortcuts

Press `?` at any time to see the help overlay.

| Key | Action |
|-----|--------|
| `tab` / `shift+tab` | Switch between tabs |
| `1`–`6` | Jump to tab by number |
| `r` | Refresh current tab data |
| `?` | Toggle help overlay |
| `q` / `ctrl+c` | Quit |
| `↑` / `↓` | Navigate form fields |
| `enter` | Next field / submit (shows confirmation) |
| `←` / `→` | Toggle strategy selector / dry-run checkbox |
| `space` | Toggle checkbox |
| `y` / `enter` | Confirm action in modal |
| `n` / `esc` | Cancel action in modal |

### Workflow Example

1. Launch: `kdctl -d goserver`
2. Review current state in the **Status** tab (auto-refreshes every 3 seconds)
3. Press `3` to switch to the **Deploy** tab
4. Type the new image (e.g. `goserver:v2`), pick strategy, set options
5. Press `↓` to navigate to **Deploy** button, then `enter`
6. A **confirmation modal** appears — press `y` to confirm
7. Watch real-time deploy events stream in the **Events** section
8. After completion, check **Status** and **History** tabs for updated state
9. If something goes wrong, use the **Rollback** tab to revert

---

## kdctl Subcommands Reference

### `kdctl` / `kdctl ui` — Interactive TUI

```bash
kdctl -n <namespace> -d <deployment>
kdctl ui -n <namespace> -d <deployment>
```

Launches the Bubble Tea TUI targeting a specific Kubernetes deployment. Talks directly to the cluster via kubeconfig.

### `kdctl start` — gRPC Server

```bash
kdctl start [--port 9090] [--host 0.0.0.0] [--log-format console] [--log-level debug]
```

Starts the kube-deploy gRPC server for remote or multi-user access. Useful for CI pipelines, shared environments, or when running inside a cluster.

```bash
# Local development
kdctl start --port 9090 --log-format console --log-level debug

# In-cluster
kdctl start --in-cluster --port 9090
```

### `kdctl deploy` — Deploy

```bash
kdctl deploy -d <deployment> --image <image> [flags]
```

| Flag | Description | Default |
|---|---|---|
| `--deployment, -d` | Deployment name (required) | — |
| `--image` | Target container image (required) | — |
| `--strategy` | `rolling` or `canary` | `rolling` |
| `--container, -c` | Container name (multi-container pods) | — |
| `--max-unavailable` | Max unavailable pods | `0` |
| `--max-surge` | Max surge pods | `1` |
| `--canary-replicas` | Number of canary replicas | `1` |
| `--analysis-duration` | Canary health analysis duration | `60s` |
| `--success-threshold` | Consecutive successes for canary promotion | `3` |
| `--dry-run` | Preview without applying | `false` |
| `--timeout` | Deployment timeout | `5m` |

### `kdctl status` — Deployment Status

```bash
kdctl status -d <deployment> [-w] [-i 2s]
```

Shows current deployment state: phase, image, replicas, revision, health, conditions, and pods.

Use `--watch` to continuously poll.

### `kdctl health` — Health Check

```bash
kdctl health -d <deployment> [-w] [-i 5s]
```

Shows overall health status, ready pod count, progress bar, and per-pod details.

Use `--watch` to continuously poll.

### `kdctl rollback` — Rollback

```bash
kdctl rollback -d <deployment> [--revision <N>] [--reason "..."]
```

Rolls back to a specific revision (or previous if `--revision 0`).

### `kdctl history` — Revision History

```bash
kdctl history -d <deployment> [--limit 10]
```

Shows the deployment's revision history with images, replica counts, timestamps, and rollback notes.

### `kdctl version` — Version Info

```bash
kdctl version
```

---

## Example CLI Output

**Status:**
```
╭─ Deployment Status: default/goserver
╰─────────────────────────────────────
  Phase:           ✅ COMPLETED
  Image:           goserver:v2
  Strategy:        RollingUpdate
  Replicas:        3/3 ready, 3 updated, 3 available
  Revision:        2
  Health:          ✅ HEALTHY
  Last Updated:    2026-03-08 16:09:20

  Conditions:
    • Available=True: Deployment has minimum availability.
    • Progressing=True: ReplicaSet "goserver-6454b855b5" has successfully progressed.

  Pods:
    NAME                                          STATUS       READY   RESTARTS   IMAGE
    goserver-6454b855b5-6qtw9                     Running      ✓       0          goserver:v2
    goserver-6454b855b5-rsh8r                     Running      ✓       0          goserver:v2
    goserver-6454b855b5-xhpcc                     Running      ✓       0          goserver:v2
```

**Health:**
```
╭─ Health: default/goserver
╰──────────────────────────
  Overall: ✅ HEALTHY
  Ready:   3/3
  Progress: ██████████████████████████████ 100%

  Pods:
    ✓ goserver-6454b855b5-6qtw9                       restarts=0    goserver:v2
    ✓ goserver-6454b855b5-rsh8r                       restarts=0    goserver:v2
    ✓ goserver-6454b855b5-xhpcc                       restarts=0    goserver:v2

  Checked at: 16:10:42
```

**Deploy:**
```
╭─ Deploying default/goserver → goserver:v2 (strategy: rolling)
╰──────────────────────────────────────────────────────────────
  Deploy ID: deploy-goserver-1741441543

  ── ⏳ PENDING ──
  [16:10:43] deployment default/goserver queued with strategy rolling

  ── 🔄 IN PROGRESS ──
  [16:10:43] updating deployment default/goserver from goserver:v1 to goserver:v2 [3/3 ready]
  [16:10:45] Waiting for rollout: 1 out of 3 new replicas updated [1/3 ready]
  [16:10:48] Waiting for rollout: 2 out of 3 new replicas updated [2/3 ready]

  ── ✅ COMPLETED ──
  [16:10:52] rolling update complete: default/goserver now running goserver:v2 (3/3 ready) [3/3 ready]

  ✓ Deployment completed successfully!
```

**History:**
```
╭─ Deployment History: default/goserver
╰──────────────────────────────────────

  REVISION   IMAGE                                          REPLICAS   DEPLOYED AT          NOTES
  --------   -----                                          --------   -----------          -----
  2          goserver:v2                                    3          2026-03-08 16:10:52
  1          goserver:v1                                    0          2026-03-08 14:39:02
```

---

## Global Flags

These flags are available on all subcommands:

| Flag | Short | Description | Default |
|---|---|---|---|
| `--namespace` | `-n` | Kubernetes namespace | `default` |
| `--kubeconfig` | | Path to kubeconfig file | `~/.kube/config` or `KUBECONFIG` env |
| `--context` | | Kubernetes context to use | current context |
| `--in-cluster` | | Use in-cluster config | `false` |
| `--config` | | Path to kdctl config YAML | — |
| `--log-level` | | Log level: `debug`, `info`, `warn`, `error` | `info` |

---

## Docker Images

### kdctl

```bash
# Build the kdctl Docker image
make docker

# Or manually
docker build -t kdctl:latest \
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
```

### Option 3: Makefile targets

```bash
make deploy-all        # Deploy everything
make deploy-server     # Deploy the server stack
make deploy-goserver   # Deploy goserver
make undeploy-all      # Remove everything
```

### Option 4: Manual kubectl

```bash
kubectl apply -f deploy/k8s/namespace.yaml
kubectl apply -f deploy/k8s/serviceaccount.yaml
kubectl apply -f deploy/k8s/rbac.yaml
kubectl apply -f deploy/k8s/configmap.yaml
kubectl apply -f deploy/k8s/service.yaml
kubectl apply -f deploy/k8s/deployment.yaml

kubectl apply -f deploy/goserver/deployment.yaml
kubectl apply -f deploy/goserver/service.yaml
```

### What Gets Deployed

| Resource | Namespace | Description |
|---|---|---|
| Namespace `kube-deploy` | — | Dedicated namespace for the control plane |
| ServiceAccount `kube-deploy-server` | `kube-deploy` | Pod identity for K8s API access |
| ClusterRole `kube-deploy-server` | — | Permissions for Deployments, ReplicaSets, Pods, Services, Events |
| ClusterRoleBinding `kube-deploy-server` | — | Binds the ClusterRole to the ServiceAccount |
| ConfigMap `kube-deploy-config` | `kube-deploy` | Server config (strategies, health, rollback defaults) |
| Deployment `kube-deploy-server` | `kube-deploy` | 2-replica gRPC server with probes and security context |
| Service `kube-deploy-server` | `kube-deploy` | ClusterIP on port 9090 |

### Connecting to the Server In-Cluster

```bash
# Port-forward for local access
kubectl port-forward -n kube-deploy svc/kube-deploy-server 9090:9090
```

---

## Deployment Strategies

### Rolling Update

The default strategy. Updates the Deployment image in-place with zero-downtime guarantees:

1. Patches the Deployment with the new image, `maxUnavailable=0`, `maxSurge=1`
2. Polls rollout status until all replicas are updated and ready
3. Streams progress events to the TUI or CLI in real time
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

Health events are displayed in the TUI's Health tab and trigger automated rollback when the failure threshold is exceeded.

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

kdctl reads configuration from YAML, environment variables, and CLI flags (in increasing precedence order).

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

When running `kdctl start`, the following gRPC service is exposed:

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

The `goserver` test application is a simple Go HTTP server designed for testing kdctl:

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

# Launch the TUI in development mode
make run

# Start the gRPC server in development mode
make run-server

# See all available targets
make help
```

### Makefile Targets

| Target | Description |
|---|---|
| `build` | Build the `kdctl` binary to `bin/kdctl` |
| `install` | Install `kdctl` to `$GOPATH/bin` |
| `build-all-platforms` | Cross-compile for Linux/macOS/Windows (amd64 + arm64) |
| `run` | Build and launch the TUI |
| `run-server` | Build and start the gRPC server |
| `test` | Run unit tests |
| `test-race` | Run tests with race detector |
| `test-integration` | Run integration tests |
| `cover` | Generate HTML coverage report |
| `lint` | Run golangci-lint |
| `vet` | Run go vet |
| `fmt` | Format all Go files |
| `tidy` | Tidy and verify Go modules |
| `proto` | Generate protobuf Go code |
| `docker` | Build the kdctl Docker image |
| `docker-goserver` | Build the goserver test image |
| `deploy-all` | Deploy server + goserver to cluster |
| `deploy-server` | Deploy only the server stack |
| `deploy-goserver` | Deploy only goserver |
| `undeploy-all` | Remove everything from cluster |
| `clean` | Remove build artifacts |
| `help` | Show all targets |