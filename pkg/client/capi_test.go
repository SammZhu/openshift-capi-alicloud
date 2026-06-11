package client

import "testing"

func TestMachineTag(t *testing.T) {
	tags := []Tag{
		{Key: "kubernetes.io/cluster/c1", Value: "owned"},
		{Key: MachineNameTagKey, Value: "worker-1"},
	}
	if got := machineTag(tags); got != "worker-1" {
		t.Errorf("machineTag = %q, want worker-1", got)
	}
	if got := machineTag([]Tag{{Key: "x", Value: "y"}}); got != "" {
		t.Errorf("machineTag with no machine tag = %q, want empty", got)
	}
}

func TestToTagResourcesTags(t *testing.T) {
	in := []Tag{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}
	got := toTagResourcesTags(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Key != "a" || got[0].Value != "1" || got[1].Key != "b" || got[1].Value != "2" {
		t.Errorf("toTagResourcesTags = %+v, want [{a 1} {b 2}]", got)
	}
	if got := toTagResourcesTags(nil); len(got) != 0 {
		t.Errorf("toTagResourcesTags(nil) len = %d, want 0", len(got))
	}
}
