#!/usr/bin/env bash
# ============================================================================
# kube-deploy — One-Command Cluster Deployment Script
#
# Deploys the entire kube-deploy stack (server + goserver test app) to a
# Kubernetes cluster in the correct dependency order.
#
# Usage:
#   ./deploy/deploy.sh                    # Deploy everything
#   ./deploy/deploy.sh --server-only      # Deploy only the kube-deploy server
#   ./deploy/deploy.sh --goserver-only    # Deploy only the goserver test app
#   ./deploy/deploy.sh --dry-run          # Preview without applying
#   ./deploy/deploy.sh --delete           # Tear down everything
#
# Prerequisites:
#   - kubectl configured with a valid kubeconfig
#   - A running Kubernetes cluster (minikube, kind, EKS, GKE, etc.)
#   - Container images built and accessible:
#       docker build -t kube-deploy-server:latest .
#       docker build -t goserver:v1 -f deploy/goserver/Dockerfile deploy/goserver/
# ============================================================================

set -euo pipefail

# ── Colors ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
RESET='\033[0m'

# ── Globals ─────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
K8S_DIR="$SCRIPT_DIR/k8s"
GOSERVER_DIR="$SCRIPT_DIR/goserver"

DEPLOY_SERVER=true
DEPLOY_GOSERVER=true
DRY_RUN=false
DELETE_MODE=false
WAIT_TIMEOUT="120s"

# ── Helper Functions ────────────────────────────────────────────────────────

log_info()    { echo -e "${CYAN}[INFO]${RESET}  $*"; }
log_success() { echo -e "${GREEN}[OK]${RESET}    $*"; }
log_warn()    { echo -e "${YELLOW}[WARN]${RESET}  $*"; }
log_error()   { echo -e "${RED}[ERROR]${RESET} $*"; }
log_step()    { echo -e "\n${BOLD}${CYAN}── $* ──${RESET}"; }

usage() {
    cat <<EOF
${BOLD}kube-deploy — Cluster Deployment Script${RESET}

${YELLOW}Usage:${RESET}
  $0 [OPTIONS]

${YELLOW}Options:${RESET}
  --server-only       Deploy only the kube-deploy server stack
  --goserver-only     Deploy only the goserver test application
  --dry-run           Preview manifests without applying (kubectl diff)
  --delete            Tear down all deployed resources
  --timeout DURATION  Timeout for waiting on rollouts (default: 120s)
  -h, --help          Show this help message

${YELLOW}Examples:${RESET}
  $0                          # Deploy everything
  $0 --server-only            # Deploy server stack only
  $0 --dry-run                # Preview changes
  $0 --delete                 # Remove everything from the cluster
  $0 --timeout 180s           # Deploy with a custom rollout timeout

EOF
    exit 0
}

check_prerequisites() {
    log_step "Checking prerequisites"

    if ! command -v kubectl &>/dev/null; then
        log_error "kubectl is not installed or not in PATH"
        exit 1
    fi
    log_success "kubectl found: $(kubectl version --client --short 2>/dev/null || kubectl version --client -o yaml 2>/dev/null | head -3)"

    if ! kubectl cluster-info &>/dev/null; then
        log_error "Cannot connect to Kubernetes cluster. Check your kubeconfig."
        exit 1
    fi
    log_success "Cluster reachable: $(kubectl cluster-info 2>/dev/null | head -1)"

    # Check if required manifest directories exist
    if [ "$DEPLOY_SERVER" = true ] && [ ! -d "$K8S_DIR" ]; then
        log_error "Server manifests not found at: $K8S_DIR"
        exit 1
    fi

    if [ "$DEPLOY_GOSERVER" = true ] && [ ! -d "$GOSERVER_DIR" ]; then
        log_error "goserver manifests not found at: $GOSERVER_DIR"
        exit 1
    fi

    log_success "All prerequisites satisfied"
}

# ── Apply a manifest file with optional dry-run ────────────────────────────
apply_manifest() {
    local file="$1"
    local description="${2:-$(basename "$file")}"

    if [ ! -f "$file" ]; then
        log_warn "Manifest not found, skipping: $file"
        return 0
    fi

    if [ "$DRY_RUN" = true ]; then
        log_info "[DRY-RUN] Would apply: $description"
        kubectl apply -f "$file" --dry-run=client -o yaml 2>/dev/null | head -5
        echo "  ..."
    else
        log_info "Applying: $description"
        kubectl apply -f "$file"
    fi
}

# ── Delete a manifest file ─────────────────────────────────────────────────
delete_manifest() {
    local file="$1"
    local description="${2:-$(basename "$file")}"

    if [ ! -f "$file" ]; then
        return 0
    fi

    log_info "Deleting: $description"
    kubectl delete -f "$file" --ignore-not-found --wait=true 2>/dev/null || true
}

# ── Wait for a deployment to be ready ──────────────────────────────────────
wait_for_deployment() {
    local namespace="$1"
    local deployment="$2"

    if [ "$DRY_RUN" = true ]; then
        log_info "[DRY-RUN] Would wait for deployment $deployment in namespace $namespace"
        return 0
    fi

    log_info "Waiting for deployment/$deployment in namespace $namespace (timeout: $WAIT_TIMEOUT)..."
    if kubectl rollout status deployment/"$deployment" \
        -n "$namespace" \
        --timeout="$WAIT_TIMEOUT" 2>/dev/null; then
        log_success "deployment/$deployment is ready"
    else
        log_warn "deployment/$deployment did not become ready within $WAIT_TIMEOUT"
        log_info "Check status: kubectl get pods -n $namespace -l app.kubernetes.io/name=$deployment"
        return 1
    fi
}

# ── Deploy the kube-deploy server stack ────────────────────────────────────
deploy_server() {
    log_step "Deploying kube-deploy server stack"

    # Order matters: namespace → SA → RBAC → ConfigMap → Service → Deployment
    apply_manifest "$K8S_DIR/namespace.yaml"         "Namespace: kube-deploy"
    apply_manifest "$K8S_DIR/serviceaccount.yaml"    "ServiceAccount: kube-deploy-server"
    apply_manifest "$K8S_DIR/rbac.yaml"              "RBAC: ClusterRole + ClusterRoleBinding"
    apply_manifest "$K8S_DIR/configmap.yaml"         "ConfigMap: kube-deploy-config"
    apply_manifest "$K8S_DIR/service.yaml"           "Service: kube-deploy-server (gRPC :9090)"
    apply_manifest "$K8S_DIR/deployment.yaml"        "Deployment: kube-deploy-server (2 replicas)"

    # Wait for the server deployment to roll out
    wait_for_deployment "kube-deploy" "kube-deploy-server" || true

    log_success "kube-deploy server stack deployed"
}

# ── Deploy the goserver test application ───────────────────────────────────
deploy_goserver() {
    log_step "Deploying goserver test application"

    apply_manifest "$GOSERVER_DIR/deployment.yaml"   "Deployment: goserver (3 replicas)"
    apply_manifest "$GOSERVER_DIR/service.yaml"      "Service: goserver (HTTP :80)"

    # Wait for the goserver deployment to roll out
    wait_for_deployment "default" "goserver" || true

    log_success "goserver test application deployed"
}

# ── Delete the kube-deploy server stack ────────────────────────────────────
delete_server() {
    log_step "Deleting kube-deploy server stack"

    # Reverse order: Deployment → Service → ConfigMap → RBAC → SA → Namespace
    delete_manifest "$K8S_DIR/deployment.yaml"       "Deployment: kube-deploy-server"
    delete_manifest "$K8S_DIR/service.yaml"          "Service: kube-deploy-server"
    delete_manifest "$K8S_DIR/configmap.yaml"        "ConfigMap: kube-deploy-config"
    delete_manifest "$K8S_DIR/rbac.yaml"             "RBAC: ClusterRole + ClusterRoleBinding"
    delete_manifest "$K8S_DIR/serviceaccount.yaml"   "ServiceAccount: kube-deploy-server"
    delete_manifest "$K8S_DIR/namespace.yaml"        "Namespace: kube-deploy"

    log_success "kube-deploy server stack removed"
}

# ── Delete the goserver test application ───────────────────────────────────
delete_goserver() {
    log_step "Deleting goserver test application"

    delete_manifest "$GOSERVER_DIR/service.yaml"     "Service: goserver"
    delete_manifest "$GOSERVER_DIR/deployment.yaml"  "Deployment: goserver"

    log_success "goserver test application removed"
}

# ── Print post-deploy summary ──────────────────────────────────────────────
print_summary() {
    if [ "$DRY_RUN" = true ] || [ "$DELETE_MODE" = true ]; then
        return 0
    fi

    log_step "Deployment Summary"

    echo ""
    if [ "$DEPLOY_SERVER" = true ]; then
        echo -e "${BOLD}kube-deploy server:${RESET}"
        kubectl get pods -n kube-deploy -l app.kubernetes.io/component=server \
            -o wide --no-headers 2>/dev/null | while read -r line; do
            echo "  $line"
        done
        echo ""
        echo -e "  ${CYAN}Port-forward:${RESET}  kubectl port-forward -n kube-deploy svc/kube-deploy-server 9090:9090"
        echo -e "  ${CYAN}Logs:${RESET}          kubectl logs -n kube-deploy -l app.kubernetes.io/component=server -f"
        echo ""
    fi

    if [ "$DEPLOY_GOSERVER" = true ]; then
        echo -e "${BOLD}goserver test app:${RESET}"
        kubectl get pods -n default -l app=goserver \
            -o wide --no-headers 2>/dev/null | while read -r line; do
            echo "  $line"
        done
        echo ""
        echo -e "  ${CYAN}Port-forward:${RESET}  kubectl port-forward svc/goserver 8080:80"
        echo -e "  ${CYAN}Health check:${RESET}  curl http://localhost:8080/healthz"
        echo -e "  ${CYAN}Logs:${RESET}          kubectl logs -l app=goserver -f"
        echo ""
    fi

    echo -e "${BOLD}${GREEN}✅ Deployment complete!${RESET}"
    echo ""
    echo -e "${YELLOW}Quick start:${RESET}"
    echo "  # Connect kdctl to the server"
    echo "  kubectl port-forward -n kube-deploy svc/kube-deploy-server 9090:9090 &"
    echo ""
    echo "  # Deploy a rolling update"
    echo "  ./bin/kdctl deploy -n default -d goserver --image goserver:v2 --strategy rolling --server localhost:9090"
    echo ""
    echo "  # Watch deployment status"
    echo "  ./bin/kdctl status -n default -d goserver --watch --server localhost:9090"
    echo ""
}

# ── Parse Arguments ─────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --server-only)
            DEPLOY_SERVER=true
            DEPLOY_GOSERVER=false
            shift
            ;;
        --goserver-only)
            DEPLOY_SERVER=false
            DEPLOY_GOSERVER=true
            shift
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --delete)
            DELETE_MODE=true
            shift
            ;;
        --timeout)
            WAIT_TIMEOUT="$2"
            shift 2
            ;;
        -h|--help)
            usage
            ;;
        *)
            log_error "Unknown option: $1"
            echo "Run '$0 --help' for usage."
            exit 1
            ;;
    esac
done

# ── Main ────────────────────────────────────────────────────────────────────

echo ""
echo -e "${BOLD}${CYAN}╔══════════════════════════════════════════════════════════════╗${RESET}"
echo -e "${BOLD}${CYAN}║     kube-deploy — Zero-Downtime Deployment Pipeline        ║${RESET}"
echo -e "${BOLD}${CYAN}╚══════════════════════════════════════════════════════════════╝${RESET}"
echo ""

if [ "$DRY_RUN" = true ]; then
    log_warn "DRY-RUN mode — no changes will be applied"
fi

check_prerequisites

if [ "$DELETE_MODE" = true ]; then
    # Delete in reverse order: goserver first, then server stack
    [ "$DEPLOY_GOSERVER" = true ] && delete_goserver
    [ "$DEPLOY_SERVER" = true ]   && delete_server

    echo ""
    echo -e "${BOLD}${GREEN}✅ Teardown complete!${RESET}"
    echo ""
else
    # Deploy: server first, then goserver
    [ "$DEPLOY_SERVER" = true ]   && deploy_server
    [ "$DEPLOY_GOSERVER" = true ] && deploy_goserver

    print_summary
fi
