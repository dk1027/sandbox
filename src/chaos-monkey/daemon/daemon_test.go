package daemon

import (
	"context"
	"fmt"
	"sync"
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

type fakeTrafficShaper struct {
	mu     sync.Mutex
	applied map[string]chaosv1alpha1.ChaosConfig
	cleared []string
}

func (f *fakeTrafficShaper) Name() string { return "fake" }
func (f *fakeTrafficShaper) ResolveTargetID(_ context.Context, pod corev1.Pod) (string, error) {
	return fmt.Sprintf("%s/%s", pod.Namespace, pod.Name), nil
}
func (f *fakeTrafficShaper) Apply(_ context.Context, targetID string, config chaosv1alpha1.ChaosConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applied == nil {
		f.applied = map[string]chaosv1alpha1.ChaosConfig{}
	}
	f.applied[targetID] = config
	return nil
}
func (f *fakeTrafficShaper) Clear(_ context.Context, targetID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, targetID)
	return nil
}

func TestChaosDaemonReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = chaosv1alpha1.AddToScheme(scheme)

	localPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local-pod",
			Namespace: "default",
			Labels:    map[string]string{"app": "kafka"},
		},
		Spec: corev1.PodSpec{
			NodeName: "my-node",
		},
	}
	remotePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "remote-pod",
			Namespace: "default",
			Labels:    map[string]string{"app": "kafka"},
		},
		Spec: corev1.PodSpec{
			NodeName: "other-node",
		},
	}

	task := &chaosv1alpha1.NodeChaosTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-my-node",
			Namespace: "default",
		},
		Spec: chaosv1alpha1.NodeChaosTaskSpec{
			NodeName: "my-node",
			TargetSelectors: chaosv1alpha1.TargetSelectors{
				Namespaces:  []string{"default"},
				PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "kafka"}},
			},
			ChaosConfig: chaosv1alpha1.ChaosConfig{
				LatencyMs:      200,
				DropPercentage: 10,
			},
			EndTime: metav1.NewTime(time.Now().Add(10 * time.Minute)),
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(localPod, remotePod, task).
		Build()

	shaper := &fakeTrafficShaper{}
	d := NewChaosDaemonWithShaper(cl, scheme, zap.New(zap.UseDevMode(true)), "my-node", shaper)

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "default",
			Name:      "task-my-node",
		},
	}

	_, err := d.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Daemon reconcile failed: %v", err)
	}

	d.mu.Lock()
	injectedLocal, ok := d.ActiveInjections["default/local-pod"]
	if !ok {
		t.Error("Expected default/local-pod to be in ActiveInjections")
	} else {
		if injectedLocal.Latency != 200 || injectedLocal.Drop != 10 {
			t.Errorf("Incorrect injection parameters: %+v", injectedLocal)
		}
	}

	_, ok = d.ActiveInjections["default/remote-pod"]
	if ok {
		t.Error("Did not expect default/remote-pod to be in ActiveInjections")
	}

	if _, exists := d.ActiveTimers["task-my-node"]; !exists {
		t.Error("Expected TTL timer for task-my-node")
	}
	d.mu.Unlock()

	shaper.mu.Lock()
	if _, ok := shaper.applied["default/local-pod"]; !ok {
		t.Error("expected fake shaper to be applied for local pod")
	}
	shaper.mu.Unlock()

	task.Spec.ChaosConfig.LatencyMs = 300
	if err := cl.Update(context.Background(), task); err != nil {
		t.Fatalf("Failed to update task: %v", err)
	}

	_, err = d.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Daemon reconcile 2 failed: %v", err)
	}

	d.mu.Lock()
	injectedLocal, ok = d.ActiveInjections["default/local-pod"]
	if !ok {
		t.Fatal("Expected default/local-pod to remain in ActiveInjections")
	}
	if injectedLocal.Latency != 300 {
		t.Errorf("Expected updated latency 300, got %d", injectedLocal.Latency)
	}
	d.mu.Unlock()

	if err := cl.Delete(context.Background(), task); err != nil {
		t.Fatalf("Failed to delete task: %v", err)
	}

	_, err = d.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Daemon reconcile 3 failed: %v", err)
	}

	d.mu.Lock()
	if len(d.ActiveInjections) != 0 {
		t.Errorf("Expected injections to be cleared, got %d", len(d.ActiveInjections))
	}
	if len(d.ActiveTimers) != 0 {
		t.Errorf("Expected timers to be cleared, got %d", len(d.ActiveTimers))
	}
	d.mu.Unlock()
}
