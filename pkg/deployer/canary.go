package deployer

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/sonu/kube-deploy/internal/config"
	"github.com/sonu/kube-deploy/pkg/k8s"
	"github.com/sonu/kube-deploy/pkg/models"
)

const (
	// canaryLabelKey is the label used to distinguish canary pods from stable pods.
	canaryLabelKey = "kube-deploy/variant"
	// canaryLabelValue marks a pod as part of the canary deployment.
	canaryLabelValue = "canary"
	// stableLabelValue marks a pod as part of the stable deployment.
	stableLabelValue = "stable"
	// canaryDeploymentSuffix is appended to the original deployment name for the canary.
	canaryDeploymentSuffix = "-canary"
	// canaryManagedByLabel tracks that this deployment is managed by kube-deploy.
	canaryManagedByLabel = "kube-deploy/managed-by"
	// canarySourceLabel stores the original deployment name on the canary.
	canarySourceLabel = "kube-deploy/source-deployment"
)

// CanaryStrategy implements the Strategy interface for canary deployments.
//
// The canary strategy works by creating a separate "canary" Deployment alongside
// the existing "stable" Deployment. Traffic is split between them via shared
// Service label selectors (both canary and stable pods match the Service selector).
//
// The flow is:
//  1. Label the existing deployment's pods as "stable".
//  2. Create a small canary Deployment with the new image, labelled "canary".
//  3. Wait for canary pods to become ready.
//  4. Run health analysis on canary pods for a configurable duration.
//  5. If healthy, promote: update the stable deployment to the new image, delete canary.
//  6. If unhealthy, abort: delete the canary deployment, stable is untouched.
//
// For stepped rollouts (progressive delivery), the canary replica count is
// increased in steps according to StepWeights in the CanaryConfig, giving
// the canary progressively more traffic share.
type CanaryStrategy struct {
	client *k8s.Client
	config *config.Config
	logger *zap.Logger
}

// NewCanaryStrategy creates a new canary deployment strategy.
func NewCanaryStrategy(client *k8s.Client, cfg *config.Config, logger *zap.Logger) *CanaryStrategy {
	return &CanaryStrategy{
		client: client,
		config: cfg,
		logger: logger.Named("canary"),
	}
}

// Name returns the strategy name.
func (s *CanaryStrategy) Name() string {
	return "canary"
}

// Validate checks that the request has the required fields for a canary deployment.
func (s *CanaryStrategy) Validate(req models.DeploymentRequest) error {
	if req.Target.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if req.Target.DeploymentName == "" {
		return fmt.Errorf("deployment name is required")
	}
	if req.Target.Image == "" {
		return fmt.Errorf("image is required")
	}
	if req.CanaryConfig.CanaryReplicas <= 0 {
		return fmt.Errorf("canary replicas must be > 0, got %d", req.CanaryConfig.CanaryReplicas)
	}
	if req.CanaryConfig.AnalysisDuration <= 0 {
		return fmt.Errorf("analysis duration must be > 0, got %v", req.CanaryConfig.AnalysisDuration)
	}
	return nil
}

// Execute performs the canary deployment.
//
// Steps:
//  1. Fetch the current (stable) Deployment and record its state.
//  2. Label the stable Deployment with the stable variant label.
//  3. Create a canary Deployment with the new image and canary variant label.
//  4. Wait for canary pods to become ready.
//  5. Run health analysis for the configured duration.
//  6. If all analysis passes succeed, promote: update stable to new image, delete canary.
//  7. If analysis fails, abort: delete canary, stable remains unchanged.
func (s *CanaryStrategy) Execute(ctx context.Context, req models.DeploymentRequest, events chan<- models.DeployEvent) error {
	defer close(events)

	namespace := req.Target.Namespace
	deployName := req.Target.DeploymentName
	newImage := req.Target.Image
	canaryName := deployName + canaryDeploymentSuffix

	// ---------------------------------------------------------------
	// Step 1: Fetch current stable deployment.
	// ---------------------------------------------------------------
	stableDeploy, err := s.client.GetDeployment(ctx, namespace, deployName)
	if err != nil {
		return fmt.Errorf("fetching stable deployment: %w", err)
	}

	currentImage := ""
	if len(stableDeploy.Spec.Template.Spec.Containers) > 0 {
		currentImage = stableDeploy.Spec.Template.Spec.Containers[0].Image
	}

	if currentImage == newImage {
		evt := models.NewDeployEvent(req.DeployID, models.PhaseCompleted,
			fmt.Sprintf("deployment %s/%s already running image %s", namespace, deployName, newImage))
		evt.CurrentImage = currentImage
		evt.TargetImage = newImage
		events <- evt
		return nil
	}

	stableReplicas := int32(1)
	if stableDeploy.Spec.Replicas != nil {
		stableReplicas = *stableDeploy.Spec.Replicas
	}

	canaryReplicas := int32(req.CanaryConfig.CanaryReplicas)

	s.logger.Info("starting canary deployment",
		zap.String("deployment", deployName),
		zap.String("namespace", namespace),
		zap.String("from_image", currentImage),
		zap.String("to_image", newImage),
		zap.Int32("stable_replicas", stableReplicas),
		zap.Int32("canary_replicas", canaryReplicas),
	)

	// Send initial in-progress event.
	startEvt := models.NewDeployEvent(req.DeployID, models.PhaseInProgress,
		fmt.Sprintf("starting canary deployment for %s/%s: %s -> %s (canary replicas: %d)",
			namespace, deployName, currentImage, newImage, canaryReplicas))
	startEvt.CurrentImage = currentImage
	startEvt.TargetImage = newImage
	startEvt.DesiredReplicas = stableReplicas
	startEvt.ReadyReplicas = stableDeploy.Status.ReadyReplicas
	events <- startEvt

	// Dry run exits here.
	if req.DryRun {
		evt := models.NewDeployEvent(req.DeployID, models.PhaseCompleted,
			fmt.Sprintf("[DRY RUN] would create canary %s/%s with image %s (%d replicas)",
				namespace, canaryName, newImage, canaryReplicas))
		evt.CurrentImage = currentImage
		evt.TargetImage = newImage
		evt.DesiredReplicas = stableReplicas
		events <- evt
		return nil
	}

	// ---------------------------------------------------------------
	// Step 2: Label the stable deployment with the variant label.
	// ---------------------------------------------------------------
	err = s.client.PatchDeploymentLabels(ctx, namespace, deployName, map[string]string{
		canaryLabelKey: stableLabelValue,
	})
	if err != nil {
		return fmt.Errorf("labelling stable deployment: %w", err)
	}

	s.logger.Debug("labelled stable deployment", zap.String("deployment", deployName))

	// ---------------------------------------------------------------
	// Step 3: Create the canary Deployment.
	// ---------------------------------------------------------------
	canaryDeploy, err := s.buildCanaryDeployment(stableDeploy, canaryName, newImage, canaryReplicas, req)
	if err != nil {
		return fmt.Errorf("building canary deployment spec: %w", err)
	}

	// Clean up any leftover canary from a previous attempt.
	_ = s.client.DeleteDeployment(ctx, namespace, canaryName)
	// Brief pause to allow deletion to propagate.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}

	createdCanary, err := s.client.CreateDeployment(ctx, canaryDeploy)
	if err != nil {
		return fmt.Errorf("creating canary deployment: %w", err)
	}

	s.logger.Info("canary deployment created",
		zap.String("canary", canaryName),
		zap.String("image", newImage),
		zap.Int32("replicas", canaryReplicas),
	)

	createdEvt := models.NewDeployEvent(req.DeployID, models.PhaseInProgress,
		fmt.Sprintf("canary deployment %s/%s created with image %s (%d replicas)",
			namespace, canaryName, newImage, canaryReplicas))
	createdEvt.CurrentImage = currentImage
	createdEvt.TargetImage = newImage
	createdEvt.DesiredReplicas = stableReplicas + canaryReplicas
	events <- createdEvt

	// Ensure cleanup of the canary deployment if we exit early.
	canaryCleanedUp := false
	defer func() {
		if !canaryCleanedUp {
			s.logger.Warn("cleaning up canary deployment on exit", zap.String("canary", canaryName))
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = s.client.DeleteDeployment(cleanupCtx, namespace, canaryName)
		}
	}()

	// ---------------------------------------------------------------
	// Step 4: Wait for canary pods to become ready.
	// ---------------------------------------------------------------
	readyTimeout := s.config.Deploy.RolloutTimeout
	if readyTimeout <= 0 {
		readyTimeout = 120 * time.Second
	}
	pollInterval := s.config.Deploy.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}

	waitEvt := models.NewDeployEvent(req.DeployID, models.PhaseInProgress,
		fmt.Sprintf("waiting for canary pods to become ready (timeout: %v)", readyTimeout))
	waitEvt.CurrentImage = currentImage
	waitEvt.TargetImage = newImage
	events <- waitEvt

	_, err = s.client.WaitForRollout(ctx, namespace, canaryName, readyTimeout, pollInterval,
		func(rs *k8s.DeploymentRolloutStatus) {
			evt := models.NewDeployEvent(req.DeployID, models.PhaseInProgress,
				fmt.Sprintf("canary rollout: %d/%d ready", rs.ReadyReplicas, rs.DesiredReplicas))
			evt.CurrentImage = currentImage
			evt.TargetImage = newImage
			evt.ReadyReplicas = rs.ReadyReplicas
			evt.DesiredReplicas = rs.DesiredReplicas
			evt.UpdatedReplicas = rs.UpdatedReplicas
			evt.AvailableReplicas = rs.AvailableReplicas
			evt.Revision = rs.Revision
			events <- evt
		},
	)

	if err != nil {
		abortEvt := models.NewDeployEvent(req.DeployID, models.PhaseFailed,
			fmt.Sprintf("canary pods failed to become ready: %v — aborting", err))
		abortEvt.CurrentImage = currentImage
		abortEvt.TargetImage = newImage
		abortEvt.ErrorDetail = err.Error()
		events <- abortEvt
		// Canary will be cleaned up by deferred function.
		return fmt.Errorf("canary readiness failed: %w", err)
	}

	s.logger.Info("canary pods are ready, starting analysis",
		zap.String("canary", canaryName),
		zap.Int32("replicas", canaryReplicas),
	)

	// ---------------------------------------------------------------
	// Step 5: Run health analysis on canary pods.
	// ---------------------------------------------------------------
	analysisDuration := req.CanaryConfig.AnalysisDuration
	if analysisDuration <= 0 {
		analysisDuration = 60 * time.Second
	}
	successThreshold := req.CanaryConfig.SuccessThreshold
	if successThreshold <= 0 {
		successThreshold = 3
	}

	analysisEvt := models.NewDeployEvent(req.DeployID, models.PhaseHealthCheck,
		fmt.Sprintf("running canary health analysis for %v (success threshold: %d consecutive passes)",
			analysisDuration, successThreshold))
	analysisEvt.CurrentImage = currentImage
	analysisEvt.TargetImage = newImage
	events <- analysisEvt

	analysisResult, err := s.runCanaryAnalysis(ctx, namespace, canaryName, createdCanary,
		analysisDuration, successThreshold, pollInterval, req, events, currentImage)
	if err != nil {
		abortEvt := models.NewDeployEvent(req.DeployID, models.PhaseFailed,
			fmt.Sprintf("canary analysis error: %v — aborting", err))
		abortEvt.CurrentImage = currentImage
		abortEvt.TargetImage = newImage
		abortEvt.ErrorDetail = err.Error()
		events <- abortEvt
		return fmt.Errorf("canary analysis failed: %w", err)
	}

	if !analysisResult {
		abortEvt := models.NewDeployEvent(req.DeployID, models.PhaseFailed,
			"canary analysis failed: health checks did not pass — aborting, stable deployment unchanged")
		abortEvt.CurrentImage = currentImage
		abortEvt.TargetImage = newImage
		abortEvt.ErrorDetail = "canary health analysis failed"
		events <- abortEvt
		return fmt.Errorf("canary analysis failed: health checks did not pass threshold")
	}

	s.logger.Info("canary analysis passed, promoting to stable",
		zap.String("deployment", deployName),
		zap.String("image", newImage),
	)

	// ---------------------------------------------------------------
	// Step 6: Promote — update stable deployment to new image, delete canary.
	// ---------------------------------------------------------------
	promoteEvt := models.NewDeployEvent(req.DeployID, models.PhasePromoting,
		fmt.Sprintf("canary analysis passed — promoting %s/%s to image %s", namespace, deployName, newImage))
	promoteEvt.CurrentImage = currentImage
	promoteEvt.TargetImage = newImage
	events <- promoteEvt

	// Update the stable deployment's image.
	containerName := req.Target.ContainerName
	_, err = s.client.UpdateDeploymentImage(ctx, namespace, deployName, containerName, newImage, 0, 1)
	if err != nil {
		return fmt.Errorf("promoting stable deployment to new image: %w", err)
	}

	// Wait for the stable deployment to finish rolling out with the new image.
	promoteWaitEvt := models.NewDeployEvent(req.DeployID, models.PhasePromoting,
		fmt.Sprintf("waiting for stable deployment %s/%s to roll out with image %s", namespace, deployName, newImage))
	promoteWaitEvt.CurrentImage = currentImage
	promoteWaitEvt.TargetImage = newImage
	events <- promoteWaitEvt

	finalStatus, err := s.client.WaitForRollout(ctx, namespace, deployName, readyTimeout, pollInterval,
		func(rs *k8s.DeploymentRolloutStatus) {
			evt := models.NewDeployEvent(req.DeployID, models.PhasePromoting,
				fmt.Sprintf("stable rollout: %d/%d ready", rs.ReadyReplicas, rs.DesiredReplicas))
			evt.CurrentImage = currentImage
			evt.TargetImage = newImage
			evt.ReadyReplicas = rs.ReadyReplicas
			evt.DesiredReplicas = rs.DesiredReplicas
			evt.UpdatedReplicas = rs.UpdatedReplicas
			evt.AvailableReplicas = rs.AvailableReplicas
			evt.Revision = rs.Revision
			events <- evt
		},
	)
	if err != nil {
		return fmt.Errorf("stable deployment rollout after promotion failed: %w", err)
	}

	// Delete the canary deployment — it's no longer needed.
	s.logger.Info("deleting canary deployment after successful promotion", zap.String("canary", canaryName))
	if delErr := s.client.DeleteDeployment(ctx, namespace, canaryName); delErr != nil {
		s.logger.Warn("failed to delete canary deployment after promotion",
			zap.String("canary", canaryName),
			zap.Error(delErr),
		)
		// Non-fatal: the canary will just scale down and be garbage.
	}
	canaryCleanedUp = true

	// Remove the variant label from the stable deployment.
	err = s.client.PatchDeploymentLabels(ctx, namespace, deployName, map[string]string{
		canaryLabelKey: "",
	})
	if err != nil {
		s.logger.Warn("failed to remove variant label from stable deployment",
			zap.String("deployment", deployName),
			zap.Error(err),
		)
	}

	// ---------------------------------------------------------------
	// Step 7: Send completion event.
	// ---------------------------------------------------------------
	completeEvt := models.NewDeployEvent(req.DeployID, models.PhaseCompleted,
		fmt.Sprintf("canary deployment complete: %s/%s promoted to %s (%d/%d ready)",
			namespace, deployName, newImage, finalStatus.ReadyReplicas, finalStatus.DesiredReplicas))
	completeEvt.CurrentImage = newImage
	completeEvt.TargetImage = newImage
	completeEvt.ReadyReplicas = finalStatus.ReadyReplicas
	completeEvt.DesiredReplicas = finalStatus.DesiredReplicas
	completeEvt.UpdatedReplicas = finalStatus.UpdatedReplicas
	completeEvt.AvailableReplicas = finalStatus.AvailableReplicas
	completeEvt.Revision = finalStatus.Revision
	events <- completeEvt

	s.logger.Info("canary deployment completed successfully",
		zap.String("deployment", deployName),
		zap.String("image", newImage),
		zap.Int64("revision", finalStatus.Revision),
	)

	return nil
}

// buildCanaryDeployment constructs the canary Deployment spec from the stable
// Deployment. The canary is a near-clone of the stable, but with:
//   - A different name (suffixed with "-canary")
//   - The new container image
//   - The canary variant label
//   - A reduced replica count
//   - Annotations tracking the source deployment
func (s *CanaryStrategy) buildCanaryDeployment(
	stable *appsv1.Deployment,
	canaryName, newImage string,
	canaryReplicas int32,
	req models.DeploymentRequest,
) (*appsv1.Deployment, error) {

	// Clone the labels from the stable deployment's pod template, and add canary labels.
	podLabels := make(map[string]string)
	for k, v := range stable.Spec.Template.Labels {
		podLabels[k] = v
	}
	podLabels[canaryLabelKey] = canaryLabelValue
	podLabels[canaryManagedByLabel] = "kube-deploy"

	// The canary's own metadata labels.
	deployLabels := make(map[string]string)
	for k, v := range stable.Labels {
		deployLabels[k] = v
	}
	deployLabels[canaryLabelKey] = canaryLabelValue
	deployLabels[canaryManagedByLabel] = "kube-deploy"
	deployLabels[canarySourceLabel] = stable.Name

	// Annotations.
	annotations := make(map[string]string)
	annotations["kube-deploy/canary-source"] = stable.Name
	annotations["kube-deploy/canary-image"] = newImage
	annotations["kube-deploy/canary-created-at"] = time.Now().UTC().Format(time.RFC3339)
	annotations["kube-deploy/deploy-id"] = req.DeployID

	// Build the selector — it must match the pod template labels.
	// We use the same selector as the stable deployment but add the canary variant.
	selectorLabels := make(map[string]string)
	if stable.Spec.Selector != nil {
		for k, v := range stable.Spec.Selector.MatchLabels {
			selectorLabels[k] = v
		}
	}
	selectorLabels[canaryLabelKey] = canaryLabelValue

	// Clone the containers and update the image for the target container.
	containers := make([]corev1.Container, len(stable.Spec.Template.Spec.Containers))
	copy(containers, stable.Spec.Template.Spec.Containers)

	containerName := req.Target.ContainerName
	containerFound := false
	for i := range containers {
		if containerName == "" || containers[i].Name == containerName {
			containers[i].Image = newImage
			containerFound = true
			if containerName != "" {
				break
			}
		}
	}
	if !containerFound && containerName != "" {
		return nil, fmt.Errorf("container %q not found in stable deployment", containerName)
	}

	// Clone volumes, init containers, and other pod spec fields.
	podSpec := stable.Spec.Template.Spec.DeepCopy()
	podSpec.Containers = containers

	// Pod template annotations.
	podAnnotations := make(map[string]string)
	for k, v := range stable.Spec.Template.Annotations {
		podAnnotations[k] = v
	}
	podAnnotations["kube-deploy/variant"] = canaryLabelValue
	podAnnotations["kube-deploy/target-image"] = newImage

	canary := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        canaryName,
			Namespace:   stable.Namespace,
			Labels:      deployLabels,
			Annotations: annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &canaryReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: podAnnotations,
				},
				Spec: *podSpec,
			},
			// Use the same strategy as stable but with conservative settings.
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
		},
	}

	return canary, nil
}

// runCanaryAnalysis monitors the canary deployment's health for the specified
// duration. It checks pod readiness and restart counts at each poll interval.
//
// Returns true if the canary passes the health analysis (i.e., reaches the
// success threshold of consecutive healthy checks), false if it fails.
func (s *CanaryStrategy) runCanaryAnalysis(
	ctx context.Context,
	namespace, canaryName string,
	canaryDeploy *appsv1.Deployment,
	analysisDuration time.Duration,
	successThreshold int,
	pollInterval time.Duration,
	req models.DeploymentRequest,
	events chan<- models.DeployEvent,
	currentImage string,
) (bool, error) {

	deadline := time.After(analysisDuration)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	consecutiveSuccess := 0
	consecutiveFailure := 0
	failureThreshold := 3 // number of consecutive failures to abort early
	totalChecks := 0
	newImage := req.Target.Image

	// Capture the initial restart count so we can detect restart spikes.
	initialRestarts, err := s.client.GetTotalRestartCount(ctx, namespace, canaryName)
	if err != nil {
		s.logger.Warn("could not get initial restart count for canary", zap.Error(err))
		initialRestarts = 0
	}

	maxRestartDelta := int32(3) // if restarts increase by more than this, fail.

	s.logger.Info("starting canary analysis",
		zap.String("canary", canaryName),
		zap.Duration("duration", analysisDuration),
		zap.Int("success_threshold", successThreshold),
		zap.Int32("initial_restarts", initialRestarts),
	)

	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()

		case <-deadline:
			// Time's up. Did we reach the success threshold?
			if consecutiveSuccess >= successThreshold {
				return true, nil
			}
			s.logger.Warn("canary analysis timed out without reaching success threshold",
				zap.Int("consecutive_success", consecutiveSuccess),
				zap.Int("threshold", successThreshold),
				zap.Int("total_checks", totalChecks),
			)
			return false, nil

		case <-ticker.C:
			totalChecks++
			healthy, msg := s.checkCanaryHealth(ctx, namespace, canaryName, initialRestarts, maxRestartDelta)

			if healthy {
				consecutiveSuccess++
				consecutiveFailure = 0

				evt := models.NewDeployEvent(req.DeployID, models.PhaseHealthCheck,
					fmt.Sprintf("canary health check #%d PASSED (%d/%d consecutive): %s",
						totalChecks, consecutiveSuccess, successThreshold, msg))
				evt.CurrentImage = currentImage
				evt.TargetImage = newImage
				events <- evt

				s.logger.Debug("canary health check passed",
					zap.Int("check", totalChecks),
					zap.Int("consecutive_success", consecutiveSuccess),
					zap.String("message", msg),
				)

				if consecutiveSuccess >= successThreshold {
					s.logger.Info("canary reached success threshold",
						zap.Int("threshold", successThreshold),
					)
					return true, nil
				}
			} else {
				consecutiveFailure++
				consecutiveSuccess = 0

				evt := models.NewDeployEvent(req.DeployID, models.PhaseHealthCheck,
					fmt.Sprintf("canary health check #%d FAILED (%d consecutive failures): %s",
						totalChecks, consecutiveFailure, msg))
				evt.CurrentImage = currentImage
				evt.TargetImage = newImage
				events <- evt

				s.logger.Warn("canary health check failed",
					zap.Int("check", totalChecks),
					zap.Int("consecutive_failure", consecutiveFailure),
					zap.String("message", msg),
				)

				if consecutiveFailure >= failureThreshold {
					s.logger.Error("canary reached failure threshold, aborting",
						zap.Int("threshold", failureThreshold),
					)
					return false, nil
				}
			}
		}
	}
}

// checkCanaryHealth performs a single health check on the canary deployment.
// It verifies:
//  1. All canary pods are ready.
//  2. No excessive restart count spike.
//  3. The rollout status shows all replicas available.
//
// Returns (healthy bool, message string).
func (s *CanaryStrategy) checkCanaryHealth(
	ctx context.Context,
	namespace, canaryName string,
	initialRestarts, maxRestartDelta int32,
) (bool, string) {

	// Check rollout status.
	status, err := s.client.GetDeploymentRolloutStatus(ctx, namespace, canaryName)
	if err != nil {
		return false, fmt.Sprintf("error checking rollout status: %v", err)
	}

	if !status.Ready {
		return false, fmt.Sprintf("canary not ready: %s", status.Message)
	}

	// Check restart count delta.
	currentRestarts, err := s.client.GetTotalRestartCount(ctx, namespace, canaryName)
	if err != nil {
		return false, fmt.Sprintf("error checking restart count: %v", err)
	}

	restartDelta := currentRestarts - initialRestarts
	if restartDelta > maxRestartDelta {
		return false, fmt.Sprintf("excessive restarts detected: %d new restarts (threshold: %d)",
			restartDelta, maxRestartDelta)
	}

	// Check individual pod statuses for CrashLoopBackOff or error states.
	podStatuses, err := s.client.GetPodStatuses(ctx, namespace, canaryName)
	if err != nil {
		return false, fmt.Sprintf("error checking pod statuses: %v", err)
	}

	for _, pod := range podStatuses {
		if !pod.Ready {
			return false, fmt.Sprintf("pod %s not ready: %s", pod.Name, pod.Message)
		}
		if pod.RestartCount > 0 && pod.Message != "" {
			return false, fmt.Sprintf("pod %s has issues: %s (restarts: %d)", pod.Name, pod.Message, pod.RestartCount)
		}
	}

	return true, fmt.Sprintf("all %d canary pods healthy, %d/%d ready, restarts delta: %d",
		len(podStatuses), status.ReadyReplicas, status.DesiredReplicas, restartDelta)
}
