package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	chaosv1alpha1 "chaos_monkey/apis/chaos/v1alpha1"
)

var (
	InjectedFaults = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "chaos_monkey_injected_faults_total",
			Help: "Total number of chaos faults injected by the daemon",
		},
		[]string{"action", "node", "namespace", "pod"},
	)
	RecoveredFaults = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "chaos_monkey_recovered_faults_total",
			Help: "Total number of chaos faults recovered/cleared by the daemon",
		},
		[]string{"action", "node", "namespace", "pod"},
	)
	ActiveTasks = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "chaos_monkey_active_tasks",
			Help: "Number of active NodeChaosTasks on this node",
		},
	)
	ActiveTargets = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "chaos_monkey_active_targets",
			Help: "Number of pods currently experiencing injected chaos on this node",
		},
	)
	HeartbeatTime = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "chaos_monkey_heartbeat_timestamp_seconds",
			Help: "The Unix timestamp of the last daemon heartbeat",
		},
	)
)

func init() {
	prometheus.MustRegister(InjectedFaults)
	prometheus.MustRegister(RecoveredFaults)
	prometheus.MustRegister(ActiveTasks)
	prometheus.MustRegister(ActiveTargets)
	prometheus.MustRegister(HeartbeatTime)
}

type InjectedPod struct {
	Namespace string
	Name      string
	Action    string
	Drop      uint32
	Latency   uint32
	Corrupt   uint32
	EndTime   time.Time
	TaskName  string
	TargetID  string
}

type ChaosDaemon struct {
	client.Client
	Scheme           *runtime.Scheme
	Log              logr.Logger
	NodeName         string
	Shaper           TrafficShaper
	mu               sync.Mutex
	ActiveInjections map[string]InjectedPod
	ActiveTimers     map[string]*time.Timer
}

func NewChaosDaemon(client client.Client, scheme *runtime.Scheme, log logr.Logger, nodeName string) *ChaosDaemon {
	return NewChaosDaemonWithShaper(client, scheme, log, nodeName, NoopTrafficShaper{})
}

func NewChaosDaemonWithShaper(client client.Client, scheme *runtime.Scheme, log logr.Logger, nodeName string, shaper TrafficShaper) *ChaosDaemon {
	if shaper == nil {
		shaper = NoopTrafficShaper{}
	}
	return &ChaosDaemon{
		Client:           client,
		Scheme:           scheme,
		Log:              log,
		NodeName:         nodeName,
		Shaper:           shaper,
		ActiveInjections: make(map[string]InjectedPod),
		ActiveTimers:     make(map[string]*time.Timer),
	}
}

func (d *ChaosDaemon) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	log := d.Log.WithValues("nodechaostask", req.NamespacedName)

	// 1. Fetch NodeChaosTask
	var task chaosv1alpha1.NodeChaosTask
	err := d.Get(ctx, req.NamespacedName, &task)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("NodeChaosTask deleted. Cleaning up associated faults.", "taskName", req.Name)
			d.cleanupTaskFaults(req.Name)
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get NodeChaosTask")
		return ctrl.Result{}, err
	}

	// Verify node name
	if task.Spec.NodeName != d.NodeName {
		return ctrl.Result{}, nil
	}

	// 2. Check expiration
	now := time.Now()
	if now.After(task.Spec.EndTime.Time) {
		log.Info("NodeChaosTask is expired. Cleaning up associated faults.", "taskName", task.Name)
		d.cleanupTaskFaults(task.Name)
		return ctrl.Result{}, nil
	}

	// 3. Schedule TTL timer
	d.scheduleTTLTimer(task.Name, task.Spec.EndTime.Time)

	// 4. Evaluate matching pods on this node
	namespaces := task.Spec.TargetSelectors.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{task.Namespace}
	}

	selector, err := metav1.LabelSelectorAsSelector(&task.Spec.TargetSelectors.PodSelector)
	if err != nil {
		log.Error(err, "Failed to parse pod selector")
		return ctrl.Result{}, nil
	}

	var localPods []corev1.Pod
	for _, ns := range namespaces {
		var list corev1.PodList
		err := d.List(ctx, &list, client.InNamespace(ns), client.MatchingLabelsSelector{Selector: selector})
		if err != nil {
			log.Error(err, "Failed to list pods", "namespace", ns)
			return ctrl.Result{}, err
		}
		for _, pod := range list.Items {
			if pod.Spec.NodeName == d.NodeName {
				localPods = append(localPods, pod)
			}
		}
	}

	matchingKeys := make(map[string]bool)
	for _, pod := range localPods {
		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		matchingKeys[key] = true

		targetID, err := d.Shaper.ResolveTargetID(ctx, pod)
		if err != nil {
			log.Error(err, "Failed to resolve target ID for pod", "pod", key)
			continue
		}

		existing, injected := d.ActiveInjections[key]
		if !injected || existing.Drop != task.Spec.ChaosConfig.DropPercentage ||
			existing.Latency != task.Spec.ChaosConfig.LatencyMs ||
			existing.Corrupt != task.Spec.ChaosConfig.CorruptPercentage ||
			existing.TargetID != targetID {
			if injected && existing.TargetID != "" && existing.TargetID != targetID {
				if err := d.Shaper.Clear(ctx, existing.TargetID); err != nil {
					log.Error(err, "Failed to clear existing traffic shaping before reapply", "pod", key)
					return ctrl.Result{}, err
				}
			}
			if err := d.Shaper.Apply(ctx, targetID, task.Spec.ChaosConfig); err != nil {
				log.Error(err, "Failed to apply traffic shaping", "pod", key, "targetID", targetID)
				return ctrl.Result{}, err
			}

			d.ActiveInjections[key] = InjectedPod{
				Namespace: pod.Namespace,
				Name:      pod.Name,
				Action:    task.Spec.Action,
				Drop:      task.Spec.ChaosConfig.DropPercentage,
				Latency:   task.Spec.ChaosConfig.LatencyMs,
				Corrupt:   task.Spec.ChaosConfig.CorruptPercentage,
				EndTime:   task.Spec.EndTime.Time,
				TaskName:  task.Name,
				TargetID:  targetID,
			}

			InjectedFaults.WithLabelValues(task.Spec.Action, d.NodeName, pod.Namespace, pod.Name).Inc()
		}
	}

	// Clean up any injected pods that were configured by this task but no longer match selectors
	for key, injection := range d.ActiveInjections {
		if injection.TaskName == task.Name {
			if !matchingKeys[key] {
				d.removeInjection(key, "pod no longer matches selectors")
			}
		}
	}

	d.updateMetrics()

	timeLeft := task.Spec.EndTime.Sub(time.Now())
	if timeLeft > 0 {
		return ctrl.Result{RequeueAfter: timeLeft + 1*time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (d *ChaosDaemon) removeInjection(key string, reason string) {
	injection, exists := d.ActiveInjections[key]
	if !exists {
		return
	}
	d.Log.Info("[Chaos Daemon] DETACH & CLEAR: Revoking injected faults and detaching traffic shaping",
		"pod", key, "targetID", injection.TargetID, "reason", reason)

	if injection.TargetID != "" {
		if err := d.Shaper.Clear(context.Background(), injection.TargetID); err != nil {
			d.Log.Error(err, "Failed to clear traffic shaping", "pod", key, "targetID", injection.TargetID)
		}
	}

	RecoveredFaults.WithLabelValues(injection.Action, d.NodeName, injection.Namespace, injection.Name).Inc()
	delete(d.ActiveInjections, key)
}

func (d *ChaosDaemon) cleanupTaskFaults(taskName string) {
	for key, injection := range d.ActiveInjections {
		if injection.TaskName == taskName {
			d.removeInjection(key, "task deletion or expiration")
		}
	}
	if timer, exists := d.ActiveTimers[taskName]; exists {
		timer.Stop()
		delete(d.ActiveTimers, taskName)
	}
	d.updateMetrics()
}

func (d *ChaosDaemon) scheduleTTLTimer(taskName string, endTime time.Time) {
	if timer, exists := d.ActiveTimers[taskName]; exists {
		timer.Stop()
	}

	timeLeft := endTime.Sub(time.Now())
	if timeLeft <= 0 {
		return
	}

	d.ActiveTimers[taskName] = time.AfterFunc(timeLeft, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.Log.Info("[Chaos Daemon] Task reached absolute TTL", "taskName", taskName, "endTime", endTime)
		d.cleanupTaskFaults(taskName)
	})
}

func (d *ChaosDaemon) updateMetrics() {
	ActiveTasks.Set(float64(len(d.ActiveTimers)))
	ActiveTargets.Set(float64(len(d.ActiveInjections)))
}

func (d *ChaosDaemon) StartHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	logTicker := time.NewTicker(10 * time.Second)
	defer logTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			HeartbeatTime.Set(float64(time.Now().Unix()))
		case <-logTicker.C:
			d.mu.Lock()
			// TODO: Emit metrics instead of logging because logs is way too noisy. Our stack is using prometheus but perhaps we should use otel.
			// d.Log.Info("[Chaos Daemon Heartbeat] Running. Traffic shaping state refreshed.",
			// 	"activeTasks", len(d.ActiveTimers),
			// 	"activeTargets", len(d.ActiveInjections))
			d.mu.Unlock()
		}
	}
}

func (d *ChaosDaemon) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&chaosv1alpha1.NodeChaosTask{}).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				var taskList chaosv1alpha1.NodeChaosTaskList
				err := mgr.GetClient().List(ctx, &taskList)
				if err != nil {
					return nil
				}
				var requests []reconcile.Request
				for _, task := range taskList.Items {
					if task.Spec.NodeName == d.NodeName {
						requests = append(requests, reconcile.Request{
							NamespacedName: types.NamespacedName{
								Name:      task.Name,
								Namespace: task.Namespace,
							},
						})
					}
				}
				return requests
			}),
		).
		Complete(d)
}

