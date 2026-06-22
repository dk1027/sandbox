package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	chaosv1alpha1 "chaos_monkey/apis/chaos/v1alpha1"
)

const HashAnnotation = "chaos.dk1027.io/config-hash"

type ChaosExperimentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

func (r *ChaosExperimentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("chaosexperiment", req.NamespacedName)

	// Fetch ChaosExperiment
	var exp chaosv1alpha1.ChaosExperiment
	if err := r.Get(ctx, req.NamespacedName, &exp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get ChaosExperiment")
		return ctrl.Result{}, err
	}

	// 1. Calculate duration and endTime
	duration, err := time.ParseDuration(exp.Spec.Duration)
	if err != nil {
		log.Error(err, "Failed to parse duration", "duration", exp.Spec.Duration)
		return ctrl.Result{}, nil
	}

	// Calculate endTime relative to CreationTimestamp
	endTime := metav1.NewTime(exp.CreationTimestamp.Add(duration))

	// If the experiment is already expired, do not create/update tasks
	if time.Now().After(endTime.Time) {
		log.Info("ChaosExperiment has expired")
		// Clean up tasks on all nodes by deleting them
		var taskList chaosv1alpha1.NodeChaosTaskList
		if err := r.List(ctx, &taskList, client.InNamespace(exp.Namespace)); err == nil {
			for i := range taskList.Items {
				task := &taskList.Items[i]
				for _, ref := range task.OwnerReferences {
					if ref.UID == exp.UID {
						log.Info("Deleting expired NodeChaosTask", "taskName", task.Name)
						_ = r.Delete(ctx, task)
					}
				}
			}
		}
		return ctrl.Result{}, nil
	}

	// 2. Discover involved nodes by listing matching Pods
	namespaces := exp.Spec.TargetSelectors.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{exp.Namespace}
	}

	// Compile the label selector
	selector, err := metav1.LabelSelectorAsSelector(&exp.Spec.TargetSelectors.PodSelector)
	if err != nil {
		log.Error(err, "Failed to parse label selector")
		return ctrl.Result{}, nil
	}

	nodes := make(map[string]bool)
	for _, ns := range namespaces {
		var list corev1.PodList
		err := r.List(ctx, &list, client.InNamespace(ns), client.MatchingLabelsSelector{Selector: selector})
		if err != nil {
			log.Error(err, "Failed to list pods in namespace", "namespace", ns)
			return ctrl.Result{}, err
		}
		for _, pod := range list.Items {
			if pod.Spec.NodeName != "" {
				nodes[pod.Spec.NodeName] = true
			}
		}
	}

	log.Info("Discovered target nodes", "count", len(nodes), "nodes", nodes)

	// Translate experiment configuration to ChaosConfig
	config := chaosv1alpha1.ChaosConfig{
		DropPercentage:    exp.Spec.Parameters.DropPercentage,
		LatencyMs:         exp.Spec.Parameters.LatencyMs,
		CorruptPercentage: exp.Spec.Parameters.CorruptPercentage,
		DeadManTimestamp:  uint64(time.Now().Unix()),
	}

	// Calculate config hash without dynamic fields like DeadManTimestamp
	hashConfig := config
	hashConfig.DeadManTimestamp = 0
	configHash := r.computeHash(hashConfig, exp.Spec.TargetSelectors, endTime)

	// List existing NodeChaosTasks
	var taskList chaosv1alpha1.NodeChaosTaskList
	if err := r.List(ctx, &taskList, client.InNamespace(exp.Namespace)); err != nil {
		log.Error(err, "Failed to list existing NodeChaosTasks")
		return ctrl.Result{}, err
	}

	// Keep track of which nodes currently have tasks owned by this experiment
	existingTasks := make(map[string]*chaosv1alpha1.NodeChaosTask)
	for i := range taskList.Items {
		task := &taskList.Items[i]
		for _, ref := range task.OwnerReferences {
			if ref.UID == exp.UID {
				existingTasks[task.Spec.NodeName] = task
				break
			}
		}
	}

	// 3. Create or update tasks for active nodes
	for nodeName := range nodes {
		desiredTask := &chaosv1alpha1.NodeChaosTask{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", exp.Name, nodeName),
				Namespace: exp.Namespace,
				Annotations: map[string]string{
					HashAnnotation: configHash,
				},
			},
			Spec: chaosv1alpha1.NodeChaosTaskSpec{
				NodeName:        nodeName,
				Action:          exp.Spec.Action,
				TargetSelectors: exp.Spec.TargetSelectors,
				ChaosConfig:     config,
				EndTime:         endTime,
			},
		}

		if err := ctrl.SetControllerReference(&exp, desiredTask, r.Scheme); err != nil {
			log.Error(err, "Failed to set OwnerReference on NodeChaosTask", "node", nodeName)
			return ctrl.Result{}, err
		}

		existingTask, exists := existingTasks[nodeName]
		if !exists {
			log.Info("Creating NodeChaosTask", "nodeName", nodeName, "taskName", desiredTask.Name)
			if err := r.Create(ctx, desiredTask); err != nil {
				log.Error(err, "Failed to create NodeChaosTask", "node", nodeName)
				return ctrl.Result{}, err
			}
		} else {
			existingHash := existingTask.Annotations[HashAnnotation]
			if existingHash != configHash {
				log.Info("Updating NodeChaosTask because hash mismatched", "nodeName", nodeName, "existingHash", existingHash, "newHash", configHash)
				existingTask.Annotations[HashAnnotation] = configHash
				existingTask.Spec = desiredTask.Spec
				if err := r.Update(ctx, existingTask); err != nil {
					log.Error(err, "Failed to update NodeChaosTask", "node", nodeName)
					return ctrl.Result{}, err
				}
			}
			delete(existingTasks, nodeName)
		}
	}

	// 4. Clean up tasks on nodes that are no longer targeted
	for _, taskToDelete := range existingTasks {
		log.Info("Deleting NodeChaosTask for node that is no longer targeted", "node", taskToDelete.Spec.NodeName, "taskName", taskToDelete.Name)
		if err := r.Delete(ctx, taskToDelete); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to delete old NodeChaosTask", "taskName", taskToDelete.Name)
			return ctrl.Result{}, err
		}
	}

	// Requeue when the experiment is scheduled to expire
	timeLeft := endTime.Sub(time.Now())
	if timeLeft > 0 {
		return ctrl.Result{RequeueAfter: timeLeft + 1*time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ChaosExperimentReconciler) computeHash(config chaosv1alpha1.ChaosConfig, selectors chaosv1alpha1.TargetSelectors, endTime metav1.Time) string {
	data, _ := json.Marshal(struct {
		Config    chaosv1alpha1.ChaosConfig
		Selectors chaosv1alpha1.TargetSelectors
		EndTime   metav1.Time
	}{Config: config, Selectors: selectors, EndTime: endTime})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (r *ChaosExperimentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&chaosv1alpha1.ChaosExperiment{}).
		Owns(&chaosv1alpha1.NodeChaosTask{}).
		Complete(r)
}
