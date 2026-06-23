package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
	corev1 "k8s.io/api/core/v1"
	"golang.org/x/sys/unix"

	chaosv1alpha1 "chaos_monkey/apis/chaos/v1alpha1"
)

const (
	tcActOK   = 0
	tcActShot = 2
)

type TrafficShaper interface {
	Name() string
	ResolveTargetID(ctx context.Context, pod corev1.Pod) (string, error)
	Apply(ctx context.Context, targetID string, config chaosv1alpha1.ChaosConfig) error
	Clear(ctx context.Context, targetID string) error
}

type NoopTrafficShaper struct{}

func (NoopTrafficShaper) Name() string { return "noop" }

func (NoopTrafficShaper) ResolveTargetID(_ context.Context, pod corev1.Pod) (string, error) {
	for _, status := range pod.Status.ContainerStatuses {
		if id := containerIDFromStatus(status.ContainerID); id != "" {
			return id, nil
		}
	}
	for _, status := range pod.Status.InitContainerStatuses {
		if id := containerIDFromStatus(status.ContainerID); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("pod %s/%s has no container ID yet", pod.Namespace, pod.Name)
}

func (NoopTrafficShaper) Apply(_ context.Context, _ string, _ chaosv1alpha1.ChaosConfig) error { return nil }
func (NoopTrafficShaper) Clear(_ context.Context, _ string) error                              { return nil }

type EBPFTrafficShaper struct {
	mu      sync.Mutex
	targets map[string]*targetBinding
}

type targetBinding struct {
	dropPct uint32
	program  *ebpf.Program
	ingress  link.Link
	egress   link.Link
}

func NewEBPFTrafficShaper() *EBPFTrafficShaper {
	return &EBPFTrafficShaper{targets: make(map[string]*targetBinding)}
}

func (s *EBPFTrafficShaper) Name() string { return "ebpf-tcx" }

func (s *EBPFTrafficShaper) ResolveTargetID(_ context.Context, pod corev1.Pod) (string, error) {
	for _, status := range pod.Status.ContainerStatuses {
		if id := containerIDFromStatus(status.ContainerID); id != "" {
			pid, err := findPIDForContainerID(id)
			if err != nil {
				return "", err
			}
			ifIndex, err := hostIfIndexForContainerPID(pid)
			if err != nil {
				return "", err
			}
			return strconv.Itoa(ifIndex), nil
		}
	}
	for _, status := range pod.Status.InitContainerStatuses {
		if id := containerIDFromStatus(status.ContainerID); id != "" {
			pid, err := findPIDForContainerID(id)
			if err != nil {
				return "", err
			}
			ifIndex, err := hostIfIndexForContainerPID(pid)
			if err != nil {
				return "", err
			}
			return strconv.Itoa(ifIndex), nil
		}
	}
	return "", fmt.Errorf("pod %s/%s has no running container ID", pod.Namespace, pod.Name)
}

func (s *EBPFTrafficShaper) Apply(ctx context.Context, targetID string, config chaosv1alpha1.ChaosConfig) error {
	if config.LatencyMs != 0 || config.CorruptPercentage != 0 {
		return fmt.Errorf("eBPF shaper currently supports only packet loss/blackhole; latency=%d corrupt=%d are unsupported", config.LatencyMs, config.CorruptPercentage)
	}
	if config.DropPercentage > 100 {
		return fmt.Errorf("drop percentage must be between 0 and 100, got %d", config.DropPercentage)
	}

	if config.DropPercentage == 0 {
		return s.Clear(ctx, targetID)
	}

	ifIndex, err := strconv.Atoi(targetID)
	if err != nil {
		return fmt.Errorf("invalid target ID %q: %w", targetID, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.targets[targetID]; ok {
		if existing.dropPct == config.DropPercentage {
			return nil
		}
		s.closeBindingLocked(targetID, existing)
	}

	prog, err := loadDropProgram(config.DropPercentage)
	if err != nil {
		return err
	}

	ingress, err := link.AttachTCX(link.TCXOptions{
		Interface: ifIndex,
		Program:   prog,
		Attach:    ebpf.AttachTCXIngress,
	})
	if err != nil {
		prog.Close()
		return fmt.Errorf("attach ingress tcx to ifindex %d: %w", ifIndex, err)
	}

	egress, err := link.AttachTCX(link.TCXOptions{
		Interface: ifIndex,
		Program:   prog,
		Attach:    ebpf.AttachTCXEgress,
	})
	if err != nil {
		_ = ingress.Close()
		prog.Close()
		return fmt.Errorf("attach egress tcx to ifindex %d: %w", ifIndex, err)
	}

	s.targets[targetID] = &targetBinding{
		dropPct: config.DropPercentage,
		program:  prog,
		ingress:  ingress,
		egress:   egress,
	}
	return nil
}

func (s *EBPFTrafficShaper) Clear(_ context.Context, targetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	binding, ok := s.targets[targetID]
	if !ok {
		return nil
	}
	s.closeBindingLocked(targetID, binding)
	return nil
}

func (s *EBPFTrafficShaper) closeBindingLocked(targetID string, binding *targetBinding) {
	if binding.ingress != nil {
		_ = binding.ingress.Close()
	}
	if binding.egress != nil {
		_ = binding.egress.Close()
	}
	if binding.program != nil {
		_ = binding.program.Close()
	}
	delete(s.targets, targetID)
}

func loadDropProgram(dropPct uint32) (*ebpf.Program, error) {
	spec, err := buildDropProgramSpec(dropPct)
	if err != nil {
		return nil, err
	}
	return ebpf.NewProgram(spec)
}

func buildDropProgramSpec(dropPct uint32) (*ebpf.ProgramSpec, error) {
	if dropPct > 100 {
		return nil, fmt.Errorf("drop percentage must be 0..100, got %d", dropPct)
	}

	insns := asm.Instructions{
		asm.FnGetPrandomU32.Call(),
		asm.Mod.Imm(asm.R0, 100),
		asm.JGE.Imm(asm.R0, int32(dropPct), "pass"),
		asm.LoadImm(asm.R0, tcActShot, asm.DWord),
		asm.Return(),
		asm.LoadImm(asm.R0, tcActOK, asm.DWord).WithSymbol("pass"),
		asm.Return(),
	}

	return &ebpf.ProgramSpec{
		Name:         fmt.Sprintf("chaos_drop_%d", dropPct),
		Type:         ebpf.SchedCLS,
		License:      "GPL",
		Instructions: insns,
	}, nil
}

func hostIfIndexForContainerPID(pid int) (int, error) {
	var ifIndex int
	err := withTargetNetNS(pid, func() error {
		data, err := os.ReadFile("/sys/class/net/eth0/iflink")
		if err != nil {
			return fmt.Errorf("read host veth iflink: %w", err)
		}
		ifIndex, err = strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return fmt.Errorf("parse host veth iflink: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return ifIndex, nil
}

func withTargetNetNS(pid int, fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	original, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return fmt.Errorf("open current netns: %w", err)
	}
	defer original.Close()

	target, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "ns", "net"))
	if err != nil {
		return fmt.Errorf("open target netns for pid %d: %w", pid, err)
	}
	defer target.Close()

	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("enter target netns for pid %d: %w", pid, err)
	}
	defer func() {
		_ = unix.Setns(int(original.Fd()), unix.CLONE_NEWNET)
	}()

	return fn()
}

func containerIDFromStatus(containerID string) string {
	if containerID == "" {
		return ""
	}
	parts := strings.Split(containerID, "://")
	if len(parts) == 2 {
		return parts[1]
	}
	return containerID
}

func findPIDForContainerID(containerID string) (int, error) {
	if containerID == "" {
		return 0, fmt.Errorf("container id is empty")
	}

	entries, err := filepath.Glob("/proc/[0-9]*/cgroup")
	if err != nil {
		return 0, fmt.Errorf("list proc cgroups: %w", err)
	}
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !strings.Contains(string(data), containerID) {
			continue
		}
		base := filepath.Base(filepath.Dir(path))
		pid, err := strconv.Atoi(base)
		if err == nil {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("could not find process for container %s", containerID)
}
