package k8s

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	"github.com/sonu/kube-deploy/pkg/models"
)

// ClientConfig holds configuration for building the Kubernetes client.
type ClientConfig struct {
	Kubeconfig    string
	Context       string
	InCluster     bool
	QPS           float32
	Burst         int
	Timeout       time.Duration
	RetryAttempts int
	RetryDelay    time.Duration
}

// DefaultClientConfig returns a default configuration that tries in-cluster
// first, then falls back to ~/.kube/config.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		QPS:           50,
		Burst:         100,
		Timeout:       30 * time.Second,
		RetryAttempts: 3,
		RetryDelay:    2 * time.Second,
	}
}

// Client wraps the Kubernetes typed client and provides high-level operations
// needed by the kube-deploy deployer, health monitor, and rollback controller.
type Client struct {
	clientset     kubernetes.Interface
	apps          appsv1client.AppsV1Interface
	core          corev1client.CoreV1Interface
	logger        *zap.Logger
	retryAttempts int
	retryDelay    time.Duration
}

// NewClient creates a new Kubernetes Client from the given configuration.
func NewClient(cfg ClientConfig, logger *zap.Logger) (*Client, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	restCfg, err := buildRestConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building kubernetes rest config: %w", err)
	}

	restCfg.QPS = cfg.QPS
	restCfg.Burst = cfg.Burst
	if cfg.Timeout > 0 {
		restCfg.Timeout = cfg.Timeout
	}

	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes clientset: %w", err)
	}

	retryAttempts := cfg.RetryAttempts
	if retryAttempts <= 0 {
		retryAttempts = 3
	}
	retryDelay := cfg.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 2 * time.Second
	}

	return &Client{
		clientset:     cs,
		apps:          cs.AppsV1(),
		core:          cs.CoreV1(),
		logger:        logger.Named("k8s"),
		retryAttempts: retryAttempts,
		retryDelay:    retryDelay,
	}, nil
}

// NewClientFromClientset creates a Client from an existing kubernetes.Interface.
// Useful for testing with fake clientsets.
func NewClientFromClientset(cs kubernetes.Interface, logger *zap.Logger) *Client {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Client{
		clientset:     cs,
		apps:          cs.AppsV1(),
		core:          cs.CoreV1(),
		logger:        logger.Named("k8s"),
		retryAttempts: 3,
		retryDelay:    2 * time.Second,
	}
}

// Clientset returns the underlying kubernetes.Interface for advanced operations.
func (c *Client) Clientset() kubernetes.Interface {
	return c.clientset
}

// buildRestConfig creates a *rest.Config from the ClientConfig.
func buildRestConfig(cfg ClientConfig) (*rest.Config, error) {
	if cfg.InCluster {
		return rest.InClusterConfig()
	}

	kubeconfigPath := cfg.Kubeconfig
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("KUBECONFIG")
	}
	if kubeconfigPath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
	}

	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	overrides := &clientcmd.ConfigOverrides{}
	if cfg.Context != "" {
		overrides.CurrentContext = cfg.Context
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		overrides,
	).ClientConfig()
}

// ============================================================================
// Deployment Operations
// ============================================================================

// GetDeployment fetches a Deployment by name and namespace.
func (c *Client) GetDeployment(ctx context.Context, namespace, name string) (*appsv1.Deployment, error) {
	c.logger.Debug("getting deployment", zap.String("namespace", namespace), zap.String("name", name))
	deploy, err := c.apps.Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting deployment %s/%s: %w", namespace, name, err)
	}
	return deploy, nil
}

// ListDeployments returns all deployments in a namespace, optionally filtered by labels.
func (c *Client) ListDeployments(ctx context.Context, namespace string, labelSelector map[string]string) ([]appsv1.Deployment, error) {
	c.logger.Debug("listing deployments", zap.String("namespace", namespace))
	opts := metav1.ListOptions{}
	if len(labelSelector) > 0 {
		opts.LabelSelector = labels.Set(labelSelector).String()
	}

	list, err := c.apps.Deployments(namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing deployments in %s: %w", namespace, err)
	}
	return list.Items, nil
}

// UpdateDeploymentImage updates the container image on a Deployment using a
// strategic merge patch. This is the core operation for rolling updates.
// It sets maxUnavailable and maxSurge for zero-downtime, and returns the
// updated Deployment.
func (c *Client) UpdateDeploymentImage(
	ctx context.Context,
	namespace, name string,
	containerName, newImage string,
	maxUnavailable, maxSurge int,
) (*appsv1.Deployment, error) {
	c.logger.Info("updating deployment image",
		zap.String("namespace", namespace),
		zap.String("deployment", name),
		zap.String("container", containerName),
		zap.String("image", newImage),
		zap.Int("maxUnavailable", maxUnavailable),
		zap.Int("maxSurge", maxSurge),
	)

	var updated *appsv1.Deployment

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deploy, err := c.apps.Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("getting deployment for update: %w", err)
		}

		// Find and update the target container image.
		containerFound := false
		for i := range deploy.Spec.Template.Spec.Containers {
			container := &deploy.Spec.Template.Spec.Containers[i]
			if containerName == "" || container.Name == containerName {
				container.Image = newImage
				containerFound = true
				if containerName != "" {
					break
				}
			}
		}
		if !containerFound && containerName != "" {
			return fmt.Errorf("container %q not found in deployment %s/%s", containerName, namespace, name)
		}

		// Configure rolling update strategy for zero-downtime.
		maxUnavailableVal := intstr.FromInt32(int32(maxUnavailable))
		maxSurgeVal := intstr.FromInt32(int32(maxSurge))
		deploy.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxUnavailable: &maxUnavailableVal,
				MaxSurge:       &maxSurgeVal,
			},
		}

		// Add annotation to track the change cause.
		if deploy.Spec.Template.Annotations == nil {
			deploy.Spec.Template.Annotations = make(map[string]string)
		}
		deploy.Spec.Template.Annotations["kube-deploy/updated-at"] = time.Now().UTC().Format(time.RFC3339)
		deploy.Spec.Template.Annotations["kube-deploy/target-image"] = newImage

		if deploy.Annotations == nil {
			deploy.Annotations = make(map[string]string)
		}
		deploy.Annotations["kubernetes.io/change-cause"] = fmt.Sprintf("kube-deploy: update image to %s", newImage)

		updated, err = c.apps.Deployments(namespace).Update(ctx, deploy, metav1.UpdateOptions{})
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("updating deployment image %s/%s: %w", namespace, name, err)
	}
	return updated, nil
}

// ScaleDeployment sets the replica count of a Deployment.
func (c *Client) ScaleDeployment(ctx context.Context, namespace, name string, replicas int32) error {
	c.logger.Info("scaling deployment",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Int32("replicas", replicas),
	)

	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	_, err := c.apps.Deployments(namespace).Patch(
		ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("scaling deployment %s/%s to %d: %w", namespace, name, replicas, err)
	}
	return nil
}

// PatchDeploymentLabels adds or updates labels on a Deployment's pod template.
func (c *Client) PatchDeploymentLabels(ctx context.Context, namespace, name string, patchLabels map[string]string) error {
	c.logger.Debug("patching deployment labels",
		zap.String("namespace", namespace),
		zap.String("name", name),
	)

	labelParts := make([]string, 0, len(patchLabels))
	for k, v := range patchLabels {
		labelParts = append(labelParts, fmt.Sprintf(`"%s":"%s"`, k, v))
	}
	labelsJSON := strings.Join(labelParts, ",")
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"labels":{%s}}}}}`, labelsJSON)

	_, err := c.apps.Deployments(namespace).Patch(
		ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patching labels on deployment %s/%s: %w", namespace, name, err)
	}
	return nil
}

// DeleteDeployment deletes a Deployment by name and namespace.
func (c *Client) DeleteDeployment(ctx context.Context, namespace, name string) error {
	c.logger.Warn("deleting deployment", zap.String("namespace", namespace), zap.String("name", name))
	propagation := metav1.DeletePropagationForeground
	err := c.apps.Deployments(namespace).Delete(ctx, name, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
	if err != nil {
		return fmt.Errorf("deleting deployment %s/%s: %w", namespace, name, err)
	}
	return nil
}

// CreateDeployment creates a new Deployment (used for canary deployments).
func (c *Client) CreateDeployment(ctx context.Context, deploy *appsv1.Deployment) (*appsv1.Deployment, error) {
	c.logger.Info("creating deployment",
		zap.String("namespace", deploy.Namespace),
		zap.String("name", deploy.Name),
	)
	created, err := c.apps.Deployments(deploy.Namespace).Create(ctx, deploy, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating deployment %s/%s: %w", deploy.Namespace, deploy.Name, err)
	}
	return created, nil
}

// ============================================================================
// Deployment Status & Rollout Monitoring
// ============================================================================

// DeploymentRolloutStatus contains parsed rollout status info.
type DeploymentRolloutStatus struct {
	Ready             bool
	Message           string
	ReadyReplicas     int32
	DesiredReplicas   int32
	UpdatedReplicas   int32
	AvailableReplicas int32
	Revision          int64
	Conditions        []string
}

// GetDeploymentRolloutStatus computes the current rollout status of a Deployment,
// similar to `kubectl rollout status`.
func (c *Client) GetDeploymentRolloutStatus(ctx context.Context, namespace, name string) (*DeploymentRolloutStatus, error) {
	deploy, err := c.GetDeployment(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}

	revision := getDeploymentRevision(deploy)

	conditions := make([]string, 0, len(deploy.Status.Conditions))
	for _, cond := range deploy.Status.Conditions {
		conditions = append(conditions, fmt.Sprintf("%s=%s: %s", cond.Type, cond.Status, cond.Message))
	}

	status := &DeploymentRolloutStatus{
		ReadyReplicas:     deploy.Status.ReadyReplicas,
		DesiredReplicas:   desired,
		UpdatedReplicas:   deploy.Status.UpdatedReplicas,
		AvailableReplicas: deploy.Status.AvailableReplicas,
		Revision:          revision,
		Conditions:        conditions,
	}

	// Determine if rollout is complete.
	if deploy.Status.UpdatedReplicas == desired &&
		deploy.Status.ReadyReplicas == desired &&
		deploy.Status.AvailableReplicas == desired &&
		deploy.Status.UnavailableReplicas == 0 {
		status.Ready = true
		status.Message = fmt.Sprintf("deployment %q successfully rolled out (%d/%d ready)", name, desired, desired)
	} else {
		status.Message = fmt.Sprintf("waiting for rollout: %d/%d updated, %d/%d ready, %d/%d available",
			deploy.Status.UpdatedReplicas, desired,
			deploy.Status.ReadyReplicas, desired,
			deploy.Status.AvailableReplicas, desired,
		)
	}

	return status, nil
}

// WatchDeployment returns a watch.Interface that receives events for the named Deployment.
func (c *Client) WatchDeployment(ctx context.Context, namespace, name string) (watch.Interface, error) {
	c.logger.Debug("watching deployment", zap.String("namespace", namespace), zap.String("name", name))
	return c.apps.Deployments(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", name),
	})
}

// WaitForRollout polls the deployment status until the rollout is complete,
// a timeout occurs, or the context is cancelled. It calls the onProgress
// callback with status updates.
func (c *Client) WaitForRollout(
	ctx context.Context,
	namespace, name string,
	timeout time.Duration,
	pollInterval time.Duration,
	onProgress func(*DeploymentRolloutStatus),
) (*DeploymentRolloutStatus, error) {
	c.logger.Info("waiting for rollout",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Duration("timeout", timeout),
	)

	deadline := time.After(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			status, _ := c.GetDeploymentRolloutStatus(ctx, namespace, name)
			return status, fmt.Errorf("rollout timed out after %v: %s", timeout, status.Message)
		case <-ticker.C:
			status, err := c.GetDeploymentRolloutStatus(ctx, namespace, name)
			if err != nil {
				c.logger.Warn("error checking rollout status", zap.Error(err))
				continue
			}
			if onProgress != nil {
				onProgress(status)
			}
			if status.Ready {
				return status, nil
			}
		}
	}
}

// ============================================================================
// ReplicaSet Operations
// ============================================================================

// GetDeploymentReplicaSets returns all ReplicaSets owned by a Deployment,
// sorted by revision (most recent last).
func (c *Client) GetDeploymentReplicaSets(ctx context.Context, namespace, deploymentName string) ([]appsv1.ReplicaSet, error) {
	deploy, err := c.GetDeployment(ctx, namespace, deploymentName)
	if err != nil {
		return nil, err
	}

	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("parsing deployment selector: %w", err)
	}

	rsList, err := c.apps.ReplicaSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("listing replicasets for deployment %s/%s: %w", namespace, deploymentName, err)
	}

	// Filter by owner reference.
	owned := make([]appsv1.ReplicaSet, 0)
	for _, rs := range rsList.Items {
		for _, ownerRef := range rs.OwnerReferences {
			if ownerRef.UID == deploy.UID {
				owned = append(owned, rs)
				break
			}
		}
	}

	// Sort by revision.
	sort.Slice(owned, func(i, j int) bool {
		ri := getReplicaSetRevision(&owned[i])
		rj := getReplicaSetRevision(&owned[j])
		return ri < rj
	})

	return owned, nil
}

// GetDeploymentRevisionHistory returns the deployment history derived from ReplicaSets.
func (c *Client) GetDeploymentRevisionHistory(ctx context.Context, namespace, deploymentName string, limit int) ([]models.DeploymentRevision, error) {
	rsList, err := c.GetDeploymentReplicaSets(ctx, namespace, deploymentName)
	if err != nil {
		return nil, err
	}

	revisions := make([]models.DeploymentRevision, 0, len(rsList))
	for _, rs := range rsList {
		rev := getReplicaSetRevision(&rs)
		image := ""
		if len(rs.Spec.Template.Spec.Containers) > 0 {
			image = rs.Spec.Template.Spec.Containers[0].Image
		}

		replicas := int32(0)
		if rs.Spec.Replicas != nil {
			replicas = *rs.Spec.Replicas
		}

		deployedAt := rs.CreationTimestamp.Time
		changeCause := rs.Annotations["kubernetes.io/change-cause"]

		revision := models.DeploymentRevision{
			Revision:   rev,
			Image:      image,
			DeployedAt: deployedAt,
			Replicas:   replicas,
			Labels:     rs.Labels,
		}

		if strings.Contains(changeCause, "rollback") {
			revision.RollbackReason = changeCause
		}

		revisions = append(revisions, revision)
	}

	// Apply limit.
	if limit > 0 && len(revisions) > limit {
		revisions = revisions[len(revisions)-limit:]
	}

	return revisions, nil
}

// ============================================================================
// Pod Operations
// ============================================================================

// GetDeploymentPods returns all pods belonging to a Deployment by matching
// the Deployment's selector labels.
func (c *Client) GetDeploymentPods(ctx context.Context, namespace, deploymentName string) ([]corev1.Pod, error) {
	deploy, err := c.GetDeployment(ctx, namespace, deploymentName)
	if err != nil {
		return nil, err
	}

	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("parsing deployment selector: %w", err)
	}

	pods, err := c.core.Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("listing pods for deployment %s/%s: %w", namespace, deploymentName, err)
	}

	return pods.Items, nil
}

// GetPodStatuses returns a models.PodStatus slice for all pods in a Deployment.
func (c *Client) GetPodStatuses(ctx context.Context, namespace, deploymentName string) ([]models.PodStatus, error) {
	pods, err := c.GetDeploymentPods(ctx, namespace, deploymentName)
	if err != nil {
		return nil, err
	}

	statuses := make([]models.PodStatus, 0, len(pods))
	for _, pod := range pods {
		ps := models.PodStatus{
			Name:     pod.Name,
			Phase:    string(pod.Status.Phase),
			NodeName: pod.Spec.NodeName,
		}

		if pod.Status.StartTime != nil {
			ps.StartTime = pod.Status.StartTime.Time
		}

		// Get the primary container image and readiness/restart info.
		if len(pod.Spec.Containers) > 0 {
			ps.Image = pod.Spec.Containers[0].Image
		}

		allReady := true
		totalRestarts := int32(0)
		for _, cs := range pod.Status.ContainerStatuses {
			totalRestarts += cs.RestartCount
			if !cs.Ready {
				allReady = false
				if cs.State.Waiting != nil {
					ps.Message = fmt.Sprintf("%s: %s", cs.State.Waiting.Reason, cs.State.Waiting.Message)
				} else if cs.State.Terminated != nil {
					ps.Message = fmt.Sprintf("Terminated: %s (exit %d)", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
				}
			}
		}
		ps.Ready = allReady
		ps.RestartCount = totalRestarts

		statuses = append(statuses, ps)
	}

	return statuses, nil
}

// GetTotalRestartCount returns the total restart count across all containers
// in all pods of a Deployment.
func (c *Client) GetTotalRestartCount(ctx context.Context, namespace, deploymentName string) (int32, error) {
	pods, err := c.GetDeploymentPods(ctx, namespace, deploymentName)
	if err != nil {
		return 0, err
	}

	var total int32
	for _, pod := range pods {
		for _, cs := range pod.Status.ContainerStatuses {
			total += cs.RestartCount
		}
	}
	return total, nil
}

// ============================================================================
// Service Operations
// ============================================================================

// GetService fetches a Service by name and namespace.
func (c *Client) GetService(ctx context.Context, namespace, name string) (*corev1.Service, error) {
	svc, err := c.core.Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting service %s/%s: %w", namespace, name, err)
	}
	return svc, nil
}

// PatchServiceSelector updates the label selector on a Service.
// This is used for blue/green and canary traffic shifting.
func (c *Client) PatchServiceSelector(ctx context.Context, namespace, name string, selector map[string]string) error {
	c.logger.Info("patching service selector",
		zap.String("namespace", namespace),
		zap.String("service", name),
	)

	selectorParts := make([]string, 0, len(selector))
	for k, v := range selector {
		selectorParts = append(selectorParts, fmt.Sprintf(`"%s":"%s"`, k, v))
	}
	selectorJSON := strings.Join(selectorParts, ",")
	patch := fmt.Sprintf(`{"spec":{"selector":{%s}}}`, selectorJSON)

	_, err := c.core.Services(namespace).Patch(
		ctx, name, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patching service selector %s/%s: %w", namespace, name, err)
	}
	return nil
}

// ============================================================================
// Rollback Operations
// ============================================================================

// RollbackDeployment rolls back a Deployment to a specific revision.
// If targetRevision is 0, it rolls back to the previous revision.
func (c *Client) RollbackDeployment(ctx context.Context, namespace, name string, targetRevision int64) (*appsv1.Deployment, error) {
	c.logger.Warn("rolling back deployment",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Int64("targetRevision", targetRevision),
	)

	rsList, err := c.GetDeploymentReplicaSets(ctx, namespace, name)
	if err != nil {
		return nil, fmt.Errorf("getting replicasets for rollback: %w", err)
	}

	if len(rsList) < 2 {
		return nil, fmt.Errorf("no previous revision available for rollback")
	}

	var targetRS *appsv1.ReplicaSet
	if targetRevision == 0 {
		// Roll back to the second-to-last ReplicaSet (previous revision).
		targetRS = &rsList[len(rsList)-2]
	} else {
		// Find the ReplicaSet matching the target revision.
		for i := range rsList {
			if getReplicaSetRevision(&rsList[i]) == targetRevision {
				targetRS = &rsList[i]
				break
			}
		}
	}

	if targetRS == nil {
		return nil, fmt.Errorf("target revision %d not found in replicaset history", targetRevision)
	}

	// Perform rollback by patching the Deployment's pod template spec
	// to match the target ReplicaSet's pod template spec.
	var rolled *appsv1.Deployment
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deploy, getErr := c.apps.Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}

		// Copy pod template spec from target ReplicaSet.
		deploy.Spec.Template.Spec = targetRS.Spec.Template.Spec

		// Add rollback annotation.
		if deploy.Annotations == nil {
			deploy.Annotations = make(map[string]string)
		}
		deploy.Annotations["kubernetes.io/change-cause"] = fmt.Sprintf(
			"kube-deploy: rollback to revision %d", getReplicaSetRevision(targetRS),
		)
		if deploy.Spec.Template.Annotations == nil {
			deploy.Spec.Template.Annotations = make(map[string]string)
		}
		deploy.Spec.Template.Annotations["kube-deploy/rollback-at"] = time.Now().UTC().Format(time.RFC3339)
		deploy.Spec.Template.Annotations["kube-deploy/rollback-to-revision"] = fmt.Sprintf("%d", getReplicaSetRevision(targetRS))

		var updateErr error
		rolled, updateErr = c.apps.Deployments(namespace).Update(ctx, deploy, metav1.UpdateOptions{})
		return updateErr
	})

	if err != nil {
		return nil, fmt.Errorf("rollback deployment %s/%s to revision %d: %w", namespace, name, targetRevision, err)
	}

	return rolled, nil
}

// ============================================================================
// Events
// ============================================================================

// GetDeploymentEvents returns recent events for a Deployment.
func (c *Client) GetDeploymentEvents(ctx context.Context, namespace, deploymentName string) ([]corev1.Event, error) {
	events, err := c.core.Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Deployment", deploymentName),
	})
	if err != nil {
		return nil, fmt.Errorf("listing events for deployment %s/%s: %w", namespace, deploymentName, err)
	}
	return events.Items, nil
}

// ============================================================================
// Composite Status (combines Deployment + Pods)
// ============================================================================

// GetFullDeploymentStatus returns a complete models.DeploymentStatus including
// pod-level detail. This powers the GetDeploymentStatus gRPC endpoint.
func (c *Client) GetFullDeploymentStatus(ctx context.Context, namespace, deploymentName string) (*models.DeploymentStatus, error) {
	deploy, err := c.GetDeployment(ctx, namespace, deploymentName)
	if err != nil {
		return nil, err
	}

	podStatuses, err := c.GetPodStatuses(ctx, namespace, deploymentName)
	if err != nil {
		return nil, err
	}

	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}

	currentImage := ""
	if len(deploy.Spec.Template.Spec.Containers) > 0 {
		currentImage = deploy.Spec.Template.Spec.Containers[0].Image
	}

	conditions := make([]string, 0, len(deploy.Status.Conditions))
	for _, cond := range deploy.Status.Conditions {
		conditions = append(conditions, fmt.Sprintf("%s=%s: %s", cond.Type, cond.Status, cond.Message))
	}

	phase := resolveDeploymentPhase(deploy)
	healthStatus := resolveHealthStatus(deploy, podStatuses)

	return &models.DeploymentStatus{
		Namespace:         namespace,
		DeploymentName:    deploymentName,
		Phase:             phase,
		CurrentImage:      currentImage,
		ReadyReplicas:     deploy.Status.ReadyReplicas,
		DesiredReplicas:   desired,
		UpdatedReplicas:   deploy.Status.UpdatedReplicas,
		AvailableReplicas: deploy.Status.AvailableReplicas,
		CurrentRevision:   getDeploymentRevision(deploy),
		HealthStatus:      healthStatus,
		LastUpdated:       time.Now(),
		Pods:              podStatuses,
		Conditions:        conditions,
	}, nil
}

// ============================================================================
// Internal Helpers
// ============================================================================

// getDeploymentRevision extracts the revision annotation from a Deployment.
func getDeploymentRevision(deploy *appsv1.Deployment) int64 {
	revStr, ok := deploy.Annotations["deployment.kubernetes.io/revision"]
	if !ok {
		return 0
	}
	rev, err := strconv.ParseInt(revStr, 10, 64)
	if err != nil {
		return 0
	}
	return rev
}

// getReplicaSetRevision extracts the revision annotation from a ReplicaSet.
func getReplicaSetRevision(rs *appsv1.ReplicaSet) int64 {
	revStr, ok := rs.Annotations["deployment.kubernetes.io/revision"]
	if !ok {
		return 0
	}
	rev, err := strconv.ParseInt(revStr, 10, 64)
	if err != nil {
		return 0
	}
	return rev
}

// resolveDeploymentPhase maps the Kubernetes Deployment conditions to our
// internal DeploymentPhase model.
func resolveDeploymentPhase(deploy *appsv1.Deployment) models.DeploymentPhase {
	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}

	// Check for progress deadline exceeded.
	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse {
			return models.PhaseFailed
		}
	}

	// Check if all replicas are updated and available.
	if deploy.Status.UpdatedReplicas == desired &&
		deploy.Status.ReadyReplicas == desired &&
		deploy.Status.AvailableReplicas == desired &&
		deploy.Status.UnavailableReplicas == 0 {
		return models.PhaseCompleted
	}

	// If updatedReplicas < desired, we are still in progress.
	if deploy.Status.UpdatedReplicas > 0 || deploy.Status.UnavailableReplicas > 0 {
		return models.PhaseInProgress
	}

	return models.PhasePending
}

// resolveHealthStatus computes the overall health from pod statuses.
func resolveHealthStatus(deploy *appsv1.Deployment, pods []models.PodStatus) models.HealthStatus {
	if len(pods) == 0 {
		return models.HealthUnknown
	}

	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}

	readyCount := int32(0)
	crashLooping := false
	for _, p := range pods {
		if p.Ready {
			readyCount++
		}
		if p.RestartCount > 5 {
			crashLooping = true
		}
	}

	if crashLooping {
		return models.HealthUnhealthy
	}
	if readyCount == desired {
		return models.HealthHealthy
	}
	if readyCount > 0 {
		return models.HealthDegraded
	}
	return models.HealthUnhealthy
}
