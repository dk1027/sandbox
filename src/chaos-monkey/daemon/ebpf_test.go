package daemon

import (
	"testing"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
)

func TestBuildDropProgramSpec(t *testing.T) {
	spec, err := buildDropProgramSpec(25)
	if err != nil {
		t.Fatalf("buildDropProgramSpec returned error: %v", err)
	}
	if spec == nil {
		t.Fatal("expected non-nil program spec")
	}
	if spec.Type != ebpf.SchedCLS {
		t.Fatalf("expected SchedCLS program, got %v", spec.Type)
	}
	if spec.License != "GPL" {
		t.Fatalf("expected GPL license, got %q", spec.License)
	}
	if len(spec.Instructions) < 5 {
		t.Fatalf("expected at least 5 instructions, got %d", len(spec.Instructions))
	}
	if !spec.Instructions[0].IsBuiltinCall() {
		t.Fatalf("expected first instruction to be a helper call, got %+v", spec.Instructions[0])
	}
	if spec.Instructions[2].OpCode.JumpOp() != asm.JGE {
		t.Fatalf("expected comparison instruction to be JGE, got %s", spec.Instructions[2].OpCode)
	}
	if got := spec.Instructions[2].Constant; got != 25 {
		t.Fatalf("expected threshold 25 in comparison, got %d", got)
	}
}

func TestBuildDropProgramSpecRejectsInvalidPercentages(t *testing.T) {
	if _, err := buildDropProgramSpec(101); err == nil {
		t.Fatal("expected invalid percentage to be rejected")
	}
}
