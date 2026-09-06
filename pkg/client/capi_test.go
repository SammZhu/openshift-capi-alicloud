package client

import (
	"strings"
	"testing"
)

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

// hostNameOK gates whether a Machine name reaches RunInstances as the instance
// hostname. A name it wrongly rejects costs a readable Node name; one it wrongly
// accepts fails the create and the scale-up with it, so the rejections matter
// more than the acceptances.
func TestHostNameOK(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"machine name", "caworkers-c-abc12-xy34", true},
		{"dotted", "worker-0.cluster", true},
		{"shortest allowed", "w1", true},
		{"single character", "w", false},
		{"empty", "", false},
		{"65 characters", strings.Repeat("a", 65), false},
		{"64 characters", strings.Repeat("a", 64), true},
		{"leading hyphen", "-worker", false},
		{"trailing hyphen", "worker-", false},
		{"trailing dot", "worker.", false},
		{"doubled hyphen", "caworkers--abc12", false},
		{"doubled dot", "worker..cluster", false},
		{"hyphen then dot", "worker-.cluster", false},
		{"underscore", "worker_0", false},
		{"all digits", "12345", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostNameOK(tc.in); got != tc.want {
				t.Errorf("hostNameOK(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
