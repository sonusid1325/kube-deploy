package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/sonu/kube-deploy/api/v1"
	"github.com/sonu/kube-deploy/internal/config"
	"github.com/sonu/kube-deploy/pkg/deployer"
	"github.com/sonu/kube-deploy/pkg/health"
	"github.com/sonu/kube-deploy/pkg/k8s"
	"github.com/sonu/kube-deploy/pkg/models"
	"github.com/sonu/kube-deploy/pkg/rollback"
)

// Server is the main gRPC server that wires together the deployer engine,
// health monitor, rollback controller, and Kubernetes client to serve the
// KubeDeployService API.
type Server struct {
	v1.UnimplementedKubeDeployServiceServer

	config     *config.Config
	logger     *zap.Logger
	k8sClient  *k8s.Client
	engine     *deployer.Engine
	monitor    *health.Monitor
	rollbackC  *rollback.Controller
	grpcServer *grpc.Server

	mu       sync.RWMutex
	running  bool
	shutdown chan struct{}
}

// NewServer creates a new gRPC server with all components initialized and wired.
func NewServer(cfg *config.Config, logger *zap.Logger) (*Server, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	// Build Kubernetes client.
	k8sCfg := k8s.ClientConfig{
		Kubeconfig:    cfg.KubeconfigPath(),
		Context:       cfg.Kubernetes.Context,
		InCluster:     cfg.Kubernetes.InCluster,
		QPS:           cfg.Kubernetes.QPS,
		Burst:         cfg.Kubernetes.Burst,
		Timeout:       cfg.Kubernetes.Timeout,
		RetryAttempts: cfg.Kubernetes.RetryAttempts,
		RetryDelay:    cfg.Kubernetes.RetryDelay,
	}

	k8sClient, err := k8s.NewClient(k8sCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	// Build deployer engine.
	engine := deployer.NewEngine(k8sClient, cfg, logger)

	// Build health monitor.
	monitor := health.NewMonitor(k8sClient, logger)

	// Build rollback controller.
	rollbackPolicy := models.RollbackPolicy{
		Enabled:             cfg.Rollback.Enabled,
		MaxRetries:          cfg.Rollback.MaxRetries,
		HealthCheckInterval: cfg.Rollback.HealthCheckInterval,
		HealthCheckTimeout:  cfg.Rollback.HealthCheckTimeout,
		FailureThreshold:    cfg.Rollback.FailureThreshold,
		SuccessThreshold:    cfg.Rollback.SuccessThreshold,
	}

	rollbackCtrl := rollback.NewController(
		k8sClient,
		monitor,
		logger,
		rollback.WithDefaultPolicy(rollbackPolicy),
	)

	// Build gRPC server.
	grpcOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(cfg.Server.MaxRecvMsgSize),
		grpc.MaxSendMsgSize(cfg.Server.MaxSendMsgSize),
	}

	grpcServer := grpc.NewServer(grpcOpts...)

	s := &Server{
		config:     cfg,
		logger:     logger.Named("grpc-server"),
		k8sClient:  k8sClient,
		engine:     engine,
		monitor:    monitor,
		rollbackC:  rollbackCtrl,
		grpcServer: grpcServer,
		shutdown:   make(chan struct{}),
	}

	// Register the gRPC service.
	v1.RegisterKubeDeployServiceServer(grpcServer, s)

	// Enable gRPC reflection for debugging tools like grpcurl.
	reflection.Register(grpcServer)

	logger.Info("gRPC server initialized",
		zap.String("listen", cfg.ListenAddress()),
		zap.Bool("rollback_enabled", cfg.Rollback.Enabled),
	)

	return s, nil
}

// NewServerFromComponents creates a server from pre-built components.
// This is useful for testing where you want to inject mocks.
func NewServerFromComponents(
	cfg *config.Config,
	logger *zap.Logger,
	k8sClient *k8s.Client,
	engine *deployer.Engine,
	monitor *health.Monitor,
	rollbackCtrl *rollback.Controller,
) *Server {
	if logger == nil {
		logger = zap.NewNop()
	}

	grpcOpts := []grpc.ServerOption{}
	if cfg.Server.MaxRecvMsgSize > 0 {
		grpcOpts = append(grpcOpts, grpc.MaxRecvMsgSize(cfg.Server.MaxRecvMsgSize))
	}
	if cfg.Server.MaxSendMsgSize > 0 {
		grpcOpts = append(grpcOpts, grpc.MaxSendMsgSize(cfg.Server.MaxSendMsgSize))
	}

	grpcServer := grpc.NewServer(grpcOpts...)

	s := &Server{
		config:     cfg,
		logger:     logger.Named("grpc-server"),
		k8sClient:  k8sClient,
		engine:     engine,
		monitor:    monitor,
		rollbackC:  rollbackCtrl,
		grpcServer: grpcServer,
		shutdown:   make(chan struct{}),
	}

	v1.RegisterKubeDeployServiceServer(grpcServer, s)
	reflection.Register(grpcServer)

	return s
}

// Start begins listening on the configured address and serving gRPC requests.
// This method blocks until the server is stopped.
func (s *Server) Start() error {
	addr := s.config.ListenAddress()

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	s.logger.Info("gRPC server starting", zap.String("address", addr))

	if err := s.grpcServer.Serve(lis); err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return fmt.Errorf("serving gRPC: %w", err)
	}

	return nil
}

// Stop gracefully shuts down the gRPC server. It waits for active RPCs to
// complete up to the configured shutdown timeout, then force-stops.
func (s *Server) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	s.logger.Info("shutting down gRPC server",
		zap.Duration("timeout", s.config.Server.ShutdownTimeout),
	)

	// Stop health watches.
	s.monitor.StopAll()

	// Graceful stop with timeout.
	done := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("gRPC server stopped gracefully")
	case <-time.After(s.config.Server.ShutdownTimeout):
		s.logger.Warn("graceful shutdown timed out, forcing stop")
		s.grpcServer.Stop()
	}

	close(s.shutdown)
}

// ShutdownCh returns a channel that is closed when the server has fully stopped.
func (s *Server) ShutdownCh() <-chan struct{} {
	return s.shutdown
}

// IsRunning returns true if the server is currently serving requests.
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Engine returns the deployer engine for external access.
func (s *Server) Engine() *deployer.Engine {
	return s.engine
}

// Monitor returns the health monitor for external access.
func (s *Server) Monitor() *health.Monitor {
	return s.monitor
}

// RollbackController returns the rollback controller for external access.
func (s *Server) RollbackController() *rollback.Controller {
	return s.rollbackC
}

// ============================================================================
// gRPC Service Implementations
// ============================================================================

// Deploy initiates a deployment and streams progress events back to the client.
// The stream remains open until the deployment reaches a terminal state.
func (s *Server) Deploy(req *v1.DeployRequest, stream v1.KubeDeployService_DeployServer) error {
	if req == nil || req.Target == nil {
		return status.Error(codes.InvalidArgument, "deploy request and target are required")
	}

	s.logger.Info("Deploy RPC called",
		zap.String("deploy_id", req.DeployId),
		zap.String("namespace", req.Target.Namespace),
		zap.String("deployment", req.Target.DeploymentName),
		zap.String("image", req.Target.Image),
		zap.String("strategy", req.Strategy.String()),
	)

	// Convert the protobuf request to our internal model.
	deployReq := protoToDeploymentRequest(req)

	// Start the deployment.
	events, err := s.engine.Deploy(stream.Context(), deployReq)
	if err != nil {
		s.logger.Error("failed to start deployment", zap.Error(err))
		return status.Errorf(codes.Internal, "failed to start deployment: %v", err)
	}

	// Enable auto-rollback if the policy is enabled.
	if deployReq.RollbackPolicy.Enabled {
		err := s.rollbackC.EnableAutoRollback(
			stream.Context(),
			deployReq.Target.Namespace,
			deployReq.Target.DeploymentName,
			deployReq.RollbackPolicy,
		)
		if err != nil {
			s.logger.Warn("failed to enable auto-rollback", zap.Error(err))
		}
	}

	// Stream events to the client.
	for event := range events {
		protoEvent := deployEventToProto(event)
		if err := stream.Send(protoEvent); err != nil {
			s.logger.Warn("failed to send deploy event to client",
				zap.String("deploy_id", req.DeployId),
				zap.Error(err),
			)
			return status.Errorf(codes.Internal, "failed to send event: %v", err)
		}
	}

	return nil
}

// GetDeploymentStatus returns the current state of a specific deployment.
func (s *Server) GetDeploymentStatus(ctx context.Context, req *v1.GetDeploymentStatusRequest) (*v1.DeploymentStatus, error) {
	if req == nil || req.Namespace == "" || req.DeploymentName == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace and deployment_name are required")
	}

	s.logger.Debug("GetDeploymentStatus RPC called",
		zap.String("namespace", req.Namespace),
		zap.String("deployment", req.DeploymentName),
	)

	// First check if we have a tracker for this deployment.
	tracker, ok := s.engine.GetTrackerByDeployment(req.Namespace, req.DeploymentName)
	if ok {
		return deploymentStatusToProto(&tracker.Status), nil
	}

	// Fall back to querying Kubernetes directly.
	deployStatus, err := s.k8sClient.GetFullDeploymentStatus(ctx, req.Namespace, req.DeploymentName)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "deployment not found: %v", err)
	}

	return deploymentStatusToProto(deployStatus), nil
}

// ListDeployments returns all tracked deployments, optionally filtered by namespace.
func (s *Server) ListDeployments(ctx context.Context, req *v1.ListDeploymentsRequest) (*v1.ListDeploymentsResponse, error) {
	if req == nil {
		req = &v1.ListDeploymentsRequest{}
	}

	s.logger.Debug("ListDeployments RPC called",
		zap.String("namespace", req.Namespace),
	)

	// Get deployments from the Kubernetes cluster.
	labelSelector := req.LabelSelector
	if labelSelector == nil {
		labelSelector = make(map[string]string)
	}

	deployments, err := s.k8sClient.ListDeployments(ctx, req.Namespace, labelSelector)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "listing deployments: %v", err)
	}

	resp := &v1.ListDeploymentsResponse{
		Deployments: make([]*v1.DeploymentStatus, 0, len(deployments)),
	}

	for _, deploy := range deployments {
		fullStatus, err := s.k8sClient.GetFullDeploymentStatus(ctx, deploy.Namespace, deploy.Name)
		if err != nil {
			s.logger.Warn("failed to get full status for deployment",
				zap.String("deployment", deploy.Name),
				zap.Error(err),
			)
			continue
		}
		resp.Deployments = append(resp.Deployments, deploymentStatusToProto(fullStatus))
	}

	return resp, nil
}

// Rollback triggers a manual rollback to a specific revision.
func (s *Server) Rollback(ctx context.Context, req *v1.RollbackRequest) (*v1.RollbackResponse, error) {
	if req == nil || req.Namespace == "" || req.DeploymentName == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace and deployment_name are required")
	}

	s.logger.Info("Rollback RPC called",
		zap.String("namespace", req.Namespace),
		zap.String("deployment", req.DeploymentName),
		zap.Int64("target_revision", req.TargetRevision),
		zap.String("reason", req.Reason),
	)

	rollbackReq := models.RollbackRequest{
		Namespace:      req.Namespace,
		DeploymentName: req.DeploymentName,
		TargetRevision: req.TargetRevision,
		Reason:         req.Reason,
	}

	result, err := s.rollbackC.Rollback(ctx, rollbackReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rollback failed: %v", err)
	}

	return &v1.RollbackResponse{
		Success:              result.Success,
		Message:              result.Message,
		RolledBackToRevision: result.RolledBackToRevision,
		PreviousImage:        result.PreviousImage,
		RestoredImage:        result.RestoredImage,
		Timestamp:            timestamppb.New(result.Timestamp),
	}, nil
}

// WatchHealth opens a server-streaming connection that continuously emits
// health check results for the specified deployment.
func (s *Server) WatchHealth(req *v1.WatchHealthRequest, stream v1.KubeDeployService_WatchHealthServer) error {
	if req == nil || req.Namespace == "" || req.DeploymentName == "" {
		return status.Error(codes.InvalidArgument, "namespace and deployment_name are required")
	}

	interval := 5 * time.Second
	if req.Interval != nil {
		interval = req.Interval.AsDuration()
		if interval < 1*time.Second {
			interval = 1 * time.Second
		}
	}

	s.logger.Info("WatchHealth RPC called",
		zap.String("namespace", req.Namespace),
		zap.String("deployment", req.DeploymentName),
		zap.Duration("interval", interval),
	)

	failureThreshold := 3
	successThreshold := 1

	events, err := s.monitor.Watch(
		stream.Context(),
		req.Namespace,
		req.DeploymentName,
		interval,
		failureThreshold,
		successThreshold,
	)
	if err != nil {
		return status.Errorf(codes.Internal, "starting health watch: %v", err)
	}

	for event := range events {
		protoEvent := healthEventToProto(event)
		if err := stream.Send(protoEvent); err != nil {
			s.logger.Warn("failed to send health event to client",
				zap.String("namespace", req.Namespace),
				zap.String("deployment", req.DeploymentName),
				zap.Error(err),
			)
			return status.Errorf(codes.Internal, "failed to send event: %v", err)
		}
	}

	return nil
}

// GetHistory retrieves the deployment revision history.
func (s *Server) GetHistory(ctx context.Context, req *v1.GetHistoryRequest) (*v1.GetHistoryResponse, error) {
	if req == nil || req.Namespace == "" || req.DeploymentName == "" {
		return nil, status.Error(codes.InvalidArgument, "namespace and deployment_name are required")
	}

	s.logger.Debug("GetHistory RPC called",
		zap.String("namespace", req.Namespace),
		zap.String("deployment", req.DeploymentName),
		zap.Int32("limit", req.Limit),
	)

	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10
	}

	revisions, err := s.k8sClient.GetDeploymentRevisionHistory(ctx, req.Namespace, req.DeploymentName, limit)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "fetching history: %v", err)
	}

	resp := &v1.GetHistoryResponse{
		Revisions: make([]*v1.DeploymentRevision, 0, len(revisions)),
	}

	for _, rev := range revisions {
		protoRev := &v1.DeploymentRevision{
			Revision:       rev.Revision,
			Image:          rev.Image,
			Result:         phaseToProto(rev.Result),
			Strategy:       strategyToProto(rev.Strategy),
			DeployedAt:     timestamppb.New(rev.DeployedAt),
			DeployedBy:     rev.DeployedBy,
			RollbackReason: rev.RollbackReason,
			Labels:         rev.Labels,
			Replicas:       rev.Replicas,
		}
		if !rev.CompletedAt.IsZero() {
			protoRev.CompletedAt = timestamppb.New(rev.CompletedAt)
		}
		resp.Revisions = append(resp.Revisions, protoRev)
	}

	return resp, nil
}

// ============================================================================
// Protobuf <-> Model Converters
// ============================================================================

func protoToDeploymentRequest(req *v1.DeployRequest) models.DeploymentRequest {
	deployReq := models.DeploymentRequest{
		DeployID: req.DeployId,
		Target: models.DeploymentTarget{
			Namespace:      req.Target.Namespace,
			DeploymentName: req.Target.DeploymentName,
			ContainerName:  req.Target.ContainerName,
			Image:          req.Target.Image,
		},
		Strategy:    protoToStrategy(req.Strategy),
		Labels:      req.Labels,
		Annotations: req.Annotations,
		DryRun:      req.DryRun,
	}

	if req.RollingConfig != nil {
		deployReq.RollingConfig = models.RollingUpdateConfig{
			MaxUnavailable: int(req.RollingConfig.MaxUnavailable),
			MaxSurge:       int(req.RollingConfig.MaxSurge),
		}
	}

	if req.CanaryConfig != nil {
		deployReq.CanaryConfig = models.CanaryConfig{
			CanaryReplicas:   int(req.CanaryConfig.CanaryReplicas),
			CanaryPercent:    int(req.CanaryConfig.CanaryPercent),
			SuccessThreshold: int(req.CanaryConfig.SuccessThreshold),
			Steps:            int(req.CanaryConfig.Steps),
		}
		if req.CanaryConfig.AnalysisDuration != nil {
			deployReq.CanaryConfig.AnalysisDuration = req.CanaryConfig.AnalysisDuration.AsDuration()
		}
		if len(req.CanaryConfig.StepWeights) > 0 {
			weights := make([]int, len(req.CanaryConfig.StepWeights))
			for i, w := range req.CanaryConfig.StepWeights {
				weights[i] = int(w)
			}
			deployReq.CanaryConfig.StepWeights = weights
		}
	}

	if req.RollbackPolicy != nil {
		deployReq.RollbackPolicy = models.RollbackPolicy{
			Enabled:          req.RollbackPolicy.Enabled,
			MaxRetries:       int(req.RollbackPolicy.MaxRetries),
			FailureThreshold: int(req.RollbackPolicy.FailureThreshold),
			SuccessThreshold: int(req.RollbackPolicy.SuccessThreshold),
		}
		if req.RollbackPolicy.HealthCheckInterval != nil {
			deployReq.RollbackPolicy.HealthCheckInterval = req.RollbackPolicy.HealthCheckInterval.AsDuration()
		}
		if req.RollbackPolicy.HealthCheckTimeout != nil {
			deployReq.RollbackPolicy.HealthCheckTimeout = req.RollbackPolicy.HealthCheckTimeout.AsDuration()
		}
	}

	if len(req.HealthChecks) > 0 {
		checks := make([]models.HealthCheckConfig, 0, len(req.HealthChecks))
		for _, hc := range req.HealthChecks {
			check := models.HealthCheckConfig{
				Type:             protoToHealthCheckType(hc.Type),
				HTTPEndpoint:     hc.HttpEndpoint,
				FailureThreshold: int(hc.FailureThreshold),
				SuccessThreshold: int(hc.SuccessThreshold),
				MaxRestartCount:  int(hc.MaxRestartCount),
			}
			if hc.Interval != nil {
				check.Interval = hc.Interval.AsDuration()
			}
			if hc.Timeout != nil {
				check.Timeout = hc.Timeout.AsDuration()
			}
			checks = append(checks, check)
		}
		deployReq.HealthChecks = checks
	}

	return deployReq
}

func protoToStrategy(s v1.DeployStrategy) models.DeployStrategy {
	switch s {
	case v1.DeployStrategy_DEPLOY_STRATEGY_ROLLING:
		return models.StrategyRolling
	case v1.DeployStrategy_DEPLOY_STRATEGY_CANARY:
		return models.StrategyCanary
	case v1.DeployStrategy_DEPLOY_STRATEGY_BLUE_GREEN:
		return models.StrategyBlueGreen
	default:
		return models.StrategyRolling
	}
}

func strategyToProto(s models.DeployStrategy) v1.DeployStrategy {
	switch s {
	case models.StrategyRolling:
		return v1.DeployStrategy_DEPLOY_STRATEGY_ROLLING
	case models.StrategyCanary:
		return v1.DeployStrategy_DEPLOY_STRATEGY_CANARY
	case models.StrategyBlueGreen:
		return v1.DeployStrategy_DEPLOY_STRATEGY_BLUE_GREEN
	default:
		return v1.DeployStrategy_DEPLOY_STRATEGY_UNSPECIFIED
	}
}

func protoToHealthCheckType(t v1.HealthCheckType) models.HealthCheckType {
	switch t {
	case v1.HealthCheckType_HEALTH_CHECK_TYPE_POD_READINESS:
		return models.HealthCheckPodReadiness
	case v1.HealthCheckType_HEALTH_CHECK_TYPE_RESTART_COUNT:
		return models.HealthCheckRestartCount
	case v1.HealthCheckType_HEALTH_CHECK_TYPE_HTTP_PROBE:
		return models.HealthCheckHTTPProbe
	case v1.HealthCheckType_HEALTH_CHECK_TYPE_CUSTOM_METRIC:
		return models.HealthCheckCustomMetric
	default:
		return models.HealthCheckPodReadiness
	}
}

func healthCheckTypeToProto(t models.HealthCheckType) v1.HealthCheckType {
	switch t {
	case models.HealthCheckPodReadiness:
		return v1.HealthCheckType_HEALTH_CHECK_TYPE_POD_READINESS
	case models.HealthCheckRestartCount:
		return v1.HealthCheckType_HEALTH_CHECK_TYPE_RESTART_COUNT
	case models.HealthCheckHTTPProbe:
		return v1.HealthCheckType_HEALTH_CHECK_TYPE_HTTP_PROBE
	case models.HealthCheckCustomMetric:
		return v1.HealthCheckType_HEALTH_CHECK_TYPE_CUSTOM_METRIC
	default:
		return v1.HealthCheckType_HEALTH_CHECK_TYPE_UNSPECIFIED
	}
}

func phaseToProto(phase models.DeploymentPhase) v1.DeploymentPhase {
	switch phase {
	case models.PhasePending:
		return v1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING
	case models.PhaseInProgress:
		return v1.DeploymentPhase_DEPLOYMENT_PHASE_IN_PROGRESS
	case models.PhaseHealthCheck:
		return v1.DeploymentPhase_DEPLOYMENT_PHASE_HEALTH_CHECK
	case models.PhasePromoting:
		return v1.DeploymentPhase_DEPLOYMENT_PHASE_PROMOTING
	case models.PhaseRollingBack:
		return v1.DeploymentPhase_DEPLOYMENT_PHASE_ROLLING_BACK
	case models.PhaseCompleted:
		return v1.DeploymentPhase_DEPLOYMENT_PHASE_COMPLETED
	case models.PhaseFailed:
		return v1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED
	case models.PhaseRolledBack:
		return v1.DeploymentPhase_DEPLOYMENT_PHASE_ROLLED_BACK
	default:
		return v1.DeploymentPhase_DEPLOYMENT_PHASE_UNSPECIFIED
	}
}

func healthStatusToProto(s models.HealthStatus) v1.HealthStatus {
	switch s {
	case models.HealthHealthy:
		return v1.HealthStatus_HEALTH_STATUS_HEALTHY
	case models.HealthDegraded:
		return v1.HealthStatus_HEALTH_STATUS_DEGRADED
	case models.HealthUnhealthy:
		return v1.HealthStatus_HEALTH_STATUS_UNHEALTHY
	case models.HealthUnknown:
		return v1.HealthStatus_HEALTH_STATUS_UNKNOWN
	default:
		return v1.HealthStatus_HEALTH_STATUS_UNSPECIFIED
	}
}

func deployEventToProto(event models.DeployEvent) *v1.DeployEvent {
	return &v1.DeployEvent{
		DeployId:          event.DeployID,
		Phase:             phaseToProto(event.Phase),
		Message:           event.Message,
		Timestamp:         timestamppb.New(event.Timestamp),
		ReadyReplicas:     event.ReadyReplicas,
		DesiredReplicas:   event.DesiredReplicas,
		UpdatedReplicas:   event.UpdatedReplicas,
		AvailableReplicas: event.AvailableReplicas,
		CurrentImage:      event.CurrentImage,
		TargetImage:       event.TargetImage,
		Revision:          event.Revision,
		IsTerminal:        event.IsTerminal,
		ErrorDetail:       event.ErrorDetail,
	}
}

func deploymentStatusToProto(s *models.DeploymentStatus) *v1.DeploymentStatus {
	proto := &v1.DeploymentStatus{
		Namespace:         s.Namespace,
		DeploymentName:    s.DeploymentName,
		Phase:             phaseToProto(s.Phase),
		CurrentImage:      s.CurrentImage,
		ReadyReplicas:     s.ReadyReplicas,
		DesiredReplicas:   s.DesiredReplicas,
		UpdatedReplicas:   s.UpdatedReplicas,
		AvailableReplicas: s.AvailableReplicas,
		CurrentRevision:   s.CurrentRevision,
		HealthStatus:      healthStatusToProto(s.HealthStatus),
		LastUpdated:       timestamppb.New(s.LastUpdated),
		Conditions:        s.Conditions,
	}

	if len(s.Pods) > 0 {
		proto.Pods = make([]*v1.PodStatus, 0, len(s.Pods))
		for _, pod := range s.Pods {
			protoPod := &v1.PodStatus{
				Name:         pod.Name,
				Phase:        pod.Phase,
				Ready:        pod.Ready,
				RestartCount: pod.RestartCount,
				NodeName:     pod.NodeName,
				Image:        pod.Image,
				Message:      pod.Message,
			}
			if !pod.StartTime.IsZero() {
				protoPod.StartTime = timestamppb.New(pod.StartTime)
			}
			proto.Pods = append(proto.Pods, protoPod)
		}
	}

	return proto
}

func healthEventToProto(event models.HealthEvent) *v1.HealthEvent {
	proto := &v1.HealthEvent{
		Namespace:      event.Namespace,
		DeploymentName: event.DeploymentName,
		OverallStatus:  healthStatusToProto(event.OverallStatus),
		Timestamp:      timestamppb.New(event.Timestamp),
		Summary:        event.Summary,
	}

	if len(event.Results) > 0 {
		proto.Results = make([]*v1.HealthCheckResult, 0, len(event.Results))
		for _, result := range event.Results {
			protoResult := &v1.HealthCheckResult{
				Type:      healthCheckTypeToProto(result.Type),
				Status:    healthStatusToProto(result.Status),
				Message:   result.Message,
				Target:    result.Target,
				Latency:   durationpb.New(result.Latency),
				CheckedAt: timestamppb.New(result.CheckedAt),
				Metadata:  result.Metadata,
			}
			proto.Results = append(proto.Results, protoResult)
		}
	}

	return proto
}
