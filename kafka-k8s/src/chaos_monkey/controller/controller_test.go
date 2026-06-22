package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	chaosv1alpha1 "chaos_monkey/apis/chaos/v1alpha1"
)

func TestChaosExperimentReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = chaosv1alpha1.AddToScheme(scheme)

	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels:    map[string]string{"app": "kafka"},
		},
		Spec: corev1.PodSpec{
			NodeName: "node-a",
		},
	}
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels:    map[string]string{"app": "kafka"},
		},
		Spec: corev1.PodSpec{
			NodeName: "node-b",
		},
	}
	podPending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-pending",
			Namespace: "default",
			Labels:    map[string]string{"app": "kafka"},
		},
		Spec: corev1.PodSpec{
			NodeName: "",
		},
	}

	exp := &chaosv1alpha1.ChaosExperiment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-experiment",
			Namespace:         "default",
			CreationTimestamp: metav1.Time{Time: time.Now()},
		},
		Spec: chaosv1alpha1.ChaosExperimentSpec{
			TargetSelectors: chaosv1alpha1.TargetSelectors{
				Namespaces:  []string{"default"},
				PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "kafka"}},
			},
			Action: "NetworkDelay",
			Parameters: chaosv1alpha1.ChaosParameters{
				LatencyMs:      100,
				DropPercentage: 5,
			},
			Duration: "1m",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pod1, pod2, podPending, exp).
		Build()

	r := &ChaosExperimentReconciler{
		Client: cl,
		Scheme: scheme,
		Log:    zap.New(zap.UseDevMode(true)),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "test-experiment",
		},
	}

	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	if res.RequeueAfter == 0 {
		t.Error("Expected reconcile to requeue for TTL expiration")
	}

	var tasks chaosv1alpha1.NodeChaosTaskList
	if err := cl.List(context.Background(), &tasks); err != nil {
		t.Fatalf("Failed to list tasks: %v", err)
	}

	if len(tasks.Items) != 2 {
		t.Errorf("Expected 2 NodeChaosTasks, got %d", len(tasks.Items))
	}

	nodeTasks := make(map[string]*chaosv1alpha1.NodeChaosTask)
	for i := range tasks.Items {
		task := &tasks.Items[i]
		nodeTasks[task.Spec.NodeName] = task
	}

	taskA, ok := nodeTasks["node-a"]
	if !ok {
		t.Error("Expected NodeChaosTask for node-a")
	} else {
		if taskA.Spec.ChaosConfig.LatencyMs != 100 || taskA.Spec.ChaosConfig.DropPercentage != 5 {
			t.Errorf("Incorrect chaos config on taskA: %+v", taskA.Spec.ChaosConfig)
		}
		hash := taskA.Annotations[HashAnnotation]
		if hash == "" {
			t.Error("Expected HashAnnotation on NodeChaosTask")
		}
	}

	_, ok = nodeTasks["node-b"]
	if !ok {
		t.Error("Expected NodeChaosTask for node-b")
	}

	pod2.Spec.NodeName = "node-c"
	if err := cl.Update(context.Background(), pod2); err != nil {
		t.Fatalf("Failed to update pod2: %v", err)
	}

	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile 2 failed: %v", err)
	}

	if err := cl.List(context.Background(), &tasks); err != nil {
		t.Fatalf("Failed to list tasks: %v", err)
	}

	if len(tasks.Items) != 2 {
		t.Errorf("Expected 2 NodeChaosTasks after reschedule, got %d", len(tasks.Items))
	}

	nodeTasks2 := make(map[string]*chaosv1alpha1.NodeChaosTask)
	for i := range tasks.Items {
		task := &tasks.Items[i]
		nodeTasks2[task.Spec.NodeName] = task
	}

	if _, ok := nodeTasks2["node-b"]; ok {
		t.Error("Expected NodeChaosTask for node-b to be cleaned up")
	}
	if _, ok := nodeTasks2["node-c"]; !ok {
		t.Error("Expected NodeChaosTask for node-c to be created")
	}
}
