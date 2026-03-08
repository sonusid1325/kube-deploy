package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sonu/kube-deploy/pkg/models"
)

// ── Timer Commands ─────────────────────────────────────────────────────────

// tickCmd returns a tea.Cmd that fires a tickMsg after 3 seconds.
// Used for periodic auto-refresh of the active tab.
func tickCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// ── Status Fetch ───────────────────────────────────────────────────────────

// fetchStatusCmd returns a tea.Cmd that queries the Kubernetes API for the
// current Deployment status and its pods.
func (m *Model) fetchStatusCmd() tea.Cmd {
	m.statusLoading = true
	ns, dep := m.namespace, m.deployment
	client := m.k8sClient
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		deploy, err := client.GetDeployment(ctx, ns, dep)
		if err != nil {
			return statusDataMsg{err: err}
		}

		// Derive container image from first container.
		image := ""
		for _, c := range deploy.Spec.Template.Spec.Containers {
			image = c.Image
			break
		}

		strategyType := "RollingUpdate"
		if deploy.Spec.Strategy.Type != "" {
			strategyType = string(deploy.Spec.Strategy.Type)
		}

		// Determine phase from replica counts.
		phase := "COMPLETED"
		if deploy.Spec.Replicas != nil {
			if deploy.Status.ReadyReplicas < *deploy.Spec.Replicas {
				phase = "IN_PROGRESS"
			}
		}
		if deploy.Status.UnavailableReplicas > 0 {
			phase = "IN_PROGRESS"
		}

		// Determine health status.
		healthStatus := "HEALTHY"
		if deploy.Status.ReadyReplicas == 0 {
			healthStatus = "UNHEALTHY"
		} else if deploy.Spec.Replicas != nil && deploy.Status.ReadyReplicas < *deploy.Spec.Replicas {
			healthStatus = "DEGRADED"
		}

		// Parse revision from annotation.
		revision := int64(0)
		if v, ok := deploy.Annotations["deployment.kubernetes.io/revision"]; ok {
			if r, err := parseInt64(v); err == nil {
				revision = r
			}
		}

		// Find the most recent condition timestamp.
		var lastUpdated time.Time
		for _, cond := range deploy.Status.Conditions {
			if cond.LastUpdateTime.After(lastUpdated) {
				lastUpdated = cond.LastUpdateTime.Time
			}
		}

		// Collect human-readable conditions.
		conditions := make([]string, 0, len(deploy.Status.Conditions))
		for _, cond := range deploy.Status.Conditions {
			conditions = append(conditions,
				fmt.Sprintf("%s=%s: %s", cond.Type, string(cond.Status), cond.Message))
		}

		// Get pods via the deployment's selector.
		pods, err := client.GetDeploymentPods(ctx, ns, dep)
		if err != nil {
			return statusDataMsg{err: fmt.Errorf("fetching pods: %w", err)}
		}

		podInfos := make([]PodInfo, 0, len(pods))
		for _, pod := range pods {
			pi := PodInfo{
				Name:     pod.Name,
				Phase:    string(pod.Status.Phase),
				NodeName: pod.Spec.NodeName,
			}
			if pod.Status.StartTime != nil {
				pi.StartTime = pod.Status.StartTime.Time
			}
			for _, cs := range pod.Status.ContainerStatuses {
				pi.Ready = cs.Ready
				pi.RestartCount = cs.RestartCount
				pi.Image = cs.Image
				if cs.State.Waiting != nil {
					pi.Message = cs.State.Waiting.Reason
					if cs.State.Waiting.Message != "" {
						pi.Message += ": " + cs.State.Waiting.Message
					}
				}
				break
			}
			podInfos = append(podInfos, pi)
		}

		desired := int32(1)
		if deploy.Spec.Replicas != nil {
			desired = *deploy.Spec.Replicas
		}

		return statusDataMsg{
			deployment: DeploymentInfo{
				Name:              dep,
				Namespace:         ns,
				Image:             image,
				ReadyReplicas:     deploy.Status.ReadyReplicas,
				DesiredReplicas:   desired,
				UpdatedReplicas:   deploy.Status.UpdatedReplicas,
				AvailableReplicas: deploy.Status.AvailableReplicas,
				Revision:          revision,
				Strategy:          strategyType,
				Phase:             phase,
				HealthStatus:      healthStatus,
				LastUpdated:       lastUpdated,
			},
			pods:       podInfos,
			conditions: conditions,
			fetchedAt:  time.Now(),
		}
	}
}

// ── Health Fetch ───────────────────────────────────────────────────────────

// fetchHealthCmd returns a tea.Cmd that queries the Kubernetes API for pod
// health information.
func (m *Model) fetchHealthCmd() tea.Cmd {
	m.healthLoading = true
	ns, dep := m.namespace, m.deployment
	client := m.k8sClient
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		deploy, err := client.GetDeployment(ctx, ns, dep)
		if err != nil {
			return healthDataMsg{err: err}
		}

		pods, err := client.GetDeploymentPods(ctx, ns, dep)
		if err != nil {
			return healthDataMsg{err: fmt.Errorf("fetching pods: %w", err)}
		}

		desired := int32(1)
		if deploy.Spec.Replicas != nil {
			desired = *deploy.Spec.Replicas
		}
		ready := int(deploy.Status.ReadyReplicas)

		overall := "HEALTHY"
		if ready == 0 && int(desired) > 0 {
			overall = "UNHEALTHY"
		} else if ready < int(desired) {
			overall = "DEGRADED"
		}

		podInfos := make([]PodInfo, 0, len(pods))
		for _, pod := range pods {
			pi := PodInfo{
				Name:  pod.Name,
				Phase: string(pod.Status.Phase),
			}
			for _, cs := range pod.Status.ContainerStatuses {
				pi.Ready = cs.Ready
				pi.RestartCount = cs.RestartCount
				pi.Image = cs.Image
				if cs.State.Waiting != nil {
					pi.Message = cs.State.Waiting.Reason
				}
				break
			}
			podInfos = append(podInfos, pi)
		}

		return healthDataMsg{
			overall:   overall,
			ready:     ready,
			desired:   int(desired),
			pods:      podInfos,
			fetchedAt: time.Now(),
		}
	}
}

// ── History Fetch ──────────────────────────────────────────────────────────

// fetchHistoryCmd returns a tea.Cmd that queries the Kubernetes API for the
// deployment's revision history via ReplicaSets.
func (m *Model) fetchHistoryCmd() tea.Cmd {
	m.historyLoading = true
	ns, dep := m.namespace, m.deployment
	client := m.k8sClient
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		history, err := client.GetDeploymentRevisionHistory(ctx, ns, dep, 0)
		if err != nil {
			return historyDataMsg{err: err}
		}

		revisions := make([]RevisionInfo, 0, len(history))
		for _, rev := range history {
			ri := RevisionInfo{
				Revision:       rev.Revision,
				Image:          rev.Image,
				Replicas:       rev.Replicas,
				DeployedAt:     rev.DeployedAt,
				RollbackReason: rev.RollbackReason,
			}
			revisions = append(revisions, ri)
		}

		// Sort by revision descending (newest first).
		sort.Slice(revisions, func(i, j int) bool {
			return revisions[i].Revision > revisions[j].Revision
		})

		return historyDataMsg{revisions: revisions}
	}
}

// ── Deploy Submit ──────────────────────────────────────────────────────────

// submitDeploy validates the deploy form, builds a DeploymentRequest, and
// starts the deployment asynchronously. It stores a reference to the
// tea.Program so that individual deploy events can be forwarded via
// program.Send() for real-time streaming in the TUI.
func (m *Model) submitDeploy() tea.Cmd {
	image := strings.TrimSpace(m.deployInputs[0].Value())
	if image == "" {
		m.deployResult = "✗ Image is required"
		return nil
	}

	m.deploying = true
	m.deployResult = ""
	m.deployEvents = nil

	container := strings.TrimSpace(m.deployInputs[1].Value())
	dryRun := m.deployDryRun

	var strategy models.DeployStrategy
	var req models.DeploymentRequest

	if m.deployStrategy == 0 {
		// Rolling update strategy.
		strategy = models.StrategyRolling
		maxUnavail := parseIntOr(m.deployInputs[2].Value(), 0)
		maxSurge := parseIntOr(m.deployInputs[3].Value(), 1)
		req = models.DeploymentRequest{
			DeployID: fmt.Sprintf("deploy-%s-%d", m.deployment, time.Now().Unix()),
			Target: models.DeploymentTarget{
				Namespace:      m.namespace,
				DeploymentName: m.deployment,
				ContainerName:  container,
				Image:          image,
			},
			Strategy: strategy,
			DryRun:   dryRun,
			RollingConfig: models.RollingUpdateConfig{
				MaxUnavailable: maxUnavail,
				MaxSurge:       maxSurge,
			},
		}
	} else {
		// Canary strategy.
		strategy = models.StrategyCanary
		canaryReplicas := parseIntOr(m.deployInputs[4].Value(), 1)
		req = models.DeploymentRequest{
			DeployID: fmt.Sprintf("deploy-%s-%d", m.deployment, time.Now().Unix()),
			Target: models.DeploymentTarget{
				Namespace:      m.namespace,
				DeploymentName: m.deployment,
				ContainerName:  container,
				Image:          image,
			},
			Strategy: strategy,
			DryRun:   dryRun,
			CanaryConfig: models.CanaryConfig{
				CanaryReplicas:   canaryReplicas,
				AnalysisDuration: 60 * time.Second,
				SuccessThreshold: 3,
			},
		}
	}
	req.ApplyDefaults()

	engine := m.engine
	program := m.program // captured ref to tea.Program for Send()

	m.addLog("info", "starting %s deployment: %s → %s (dry-run: %v)",
		strategy, m.deployment, image, dryRun)

	return func() tea.Msg {
		eventsCh, err := engine.Deploy(context.Background(), req)
		if err != nil {
			return deployDoneMsg{err: err}
		}

		// Stream events into the TUI in real time via program.Send().
		// Each event is delivered as a deployEventMsg which the Update
		// loop handles to append to the event list and scroll the
		// deploy viewport.
		var lastErr error
		for event := range eventsCh {
			if program != nil {
				program.Send(deployEventMsg{event: event})
			}
			if event.Phase == models.PhaseFailed {
				lastErr = fmt.Errorf("%s", event.Message)
			}
		}
		return deployDoneMsg{err: lastErr}
	}
}

// ── Rollback Submit ────────────────────────────────────────────────────────

// submitRollback validates the rollback form and performs a rollback
// asynchronously. It fetches the current image before the rollback and
// the new image after the rollback to display the diff to the user.
func (m *Model) submitRollback() tea.Cmd {
	revStr := strings.TrimSpace(m.rollbackRevision.Value())
	reason := strings.TrimSpace(m.rollbackReason.Value())
	if reason == "" {
		reason = "manual rollback via TUI"
	}

	revision := int64(0)
	if revStr != "" {
		r, err := parseInt64(revStr)
		if err != nil {
			m.rollbackResult = "✗ Invalid revision number"
			return nil
		}
		revision = r
	}

	m.rollingBack = true
	m.rollbackResult = ""

	ns, dep := m.namespace, m.deployment
	client := m.k8sClient
	program := m.program

	m.addLog("info", "initiating rollback: %s/%s → revision %d (%s)", ns, dep, revision, reason)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		// Log progress via program.Send.
		if program != nil {
			program.Send(logStreamMsg{entry: LogEntry{
				Timestamp: time.Now(),
				Level:     "info",
				Message:   fmt.Sprintf("fetching current deployment state for %s/%s...", ns, dep),
			}})
		}

		// Get current image before rollback.
		deploy, err := client.GetDeployment(ctx, ns, dep)
		if err != nil {
			return rollbackDoneMsg{err: fmt.Errorf("getting deployment: %w", err)}
		}
		oldImage := ""
		for _, c := range deploy.Spec.Template.Spec.Containers {
			oldImage = c.Image
			break
		}

		if program != nil {
			program.Send(logStreamMsg{entry: LogEntry{
				Timestamp: time.Now(),
				Level:     "info",
				Message:   fmt.Sprintf("rolling back %s/%s from %s to revision %d...", ns, dep, oldImage, revision),
			}})
		}

		_, err = client.RollbackDeployment(ctx, ns, dep, revision)
		if err != nil {
			return rollbackDoneMsg{err: err}
		}

		// Brief delay for rollback to take effect.
		time.Sleep(2 * time.Second)

		// Get the new image after rollback.
		deploy, err = client.GetDeployment(ctx, ns, dep)
		newImage := ""
		if err == nil {
			for _, c := range deploy.Spec.Template.Spec.Containers {
				newImage = c.Image
				break
			}
		}

		return rollbackDoneMsg{
			success:  true,
			message:  "Rollback completed",
			revision: revision,
			oldImage: oldImage,
			newImage: newImage,
		}
	}
}

// ── Tab Switch & Refresh ───────────────────────────────────────────────────

// onTabSwitch is called when the user changes tabs. It clears transient
// state and refreshes the new tab's data.
func (m *Model) onTabSwitch() tea.Cmd {
	m.lastError = ""
	switch m.activeTab {
	case TabDeploy:
		m.updateDeployInputFocus()
	case TabRollback:
		m.updateRollbackInputFocus()
	}
	return m.refreshCurrentTab()
}

// refreshCurrentTab returns a tea.Cmd that fetches fresh data for whichever
// tab is currently active.
func (m *Model) refreshCurrentTab() tea.Cmd {
	switch m.activeTab {
	case TabStatus:
		return m.fetchStatusCmd()
	case TabHealth:
		return m.fetchHealthCmd()
	case TabHistory:
		return m.fetchHistoryCmd()
	}
	return nil
}
