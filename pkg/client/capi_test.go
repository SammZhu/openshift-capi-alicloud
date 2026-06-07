package client

import "testing"

func TestMachineTag(t *testing.T) {
	tags := []Tag{
		{Key: "kubernetes.io/cluster/c1", Value: "owned"},
		{Key: machineNameTagKey, Value: "worker-1"},
	}
	if got := machineTag(tags); got != "worker-1" {
		t.Errorf("machineTag = %q, want worker-1", got)
	}
	if got := machineTag([]Tag{{Key: "x", Value: "y"}}); got != "" {
		t.Errorf("machineTag with no machine tag = %q, want empty", got)
	}
}
