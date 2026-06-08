package client

import (
	"testing"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
)

// envFunc builds a getenv stub from a map.
func envFunc(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveCredential_Precedence(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string // "ramExplicit" | "ak" | "ramAuto"
		role string // expected RAM role name (for ram cases)
	}{
		{
			name: "explicit RAM role wins over AK/SK",
			env: map[string]string{
				"ALIBABA_CLOUD_ECS_METADATA":      "capa-controller-role",
				"ALIBABA_CLOUD_ACCESS_KEY_ID":     "id",
				"ALIBABA_CLOUD_ACCESS_KEY_SECRET": "secret",
			},
			want: "ramExplicit", role: "capa-controller-role",
		},
		{
			name: "static AccessKey when both AK and SK set (no RAM env)",
			env: map[string]string{
				"ALIBABA_CLOUD_ACCESS_KEY_ID":     "id",
				"ALIBABA_CLOUD_ACCESS_KEY_SECRET": "secret",
			},
			want: "ak",
		},
		{
			name: "older ALIBABACLOUD_* spelling accepted",
			env: map[string]string{
				"ALIBABACLOUD_ACCESS_KEY_ID":     "id",
				"ALIBABACLOUD_ACCESS_KEY_SECRET": "secret",
			},
			want: "ak",
		},
		{
			name: "partial AccessKey ignored -> auto RAM role",
			env:  map[string]string{"ALIBABA_CLOUD_ACCESS_KEY_ID": "id"},
			want: "ramAuto", role: "",
		},
		{
			name: "nothing set -> auto-discovered RAM role (production default)",
			env:  map[string]string{},
			want: "ramAuto", role: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cred := resolveCredentialFrom(envFunc(tc.env))
			switch tc.want {
			case "ak":
				if _, ok := cred.(*credentials.AccessKeyCredential); !ok {
					t.Fatalf("want AccessKeyCredential, got %T", cred)
				}
			case "ramExplicit", "ramAuto":
				rc, ok := cred.(*credentials.EcsRamRoleCredential)
				if !ok {
					t.Fatalf("want EcsRamRoleCredential, got %T", cred)
				}
				if rc.RoleName != tc.role {
					t.Errorf("role = %q, want %q", rc.RoleName, tc.role)
				}
			}
		})
	}
}
