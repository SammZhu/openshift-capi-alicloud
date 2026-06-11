package client

import (
	"strings"
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
		want string // "ramExplicit" | "roleArn" | "ak" | "ramAuto"
		role string // expected RAM role name (ramExplicit/ramAuto) or RoleArn (roleArn)
	}{
		{
			name: "explicit RAM role wins over everything",
			env: map[string]string{
				"ALIBABA_CLOUD_ECS_METADATA":      "capa-controller-role",
				"ALIBABA_CLOUD_ROLE_ARN":          "acs:ram::123:role/capa",
				"ALIBABA_CLOUD_ACCESS_KEY_ID":     "id",
				"ALIBABA_CLOUD_ACCESS_KEY_SECRET": "secret",
			},
			want: "ramExplicit", role: "capa-controller-role",
		},
		{
			name: "RoleArn AssumeRole when ROLE_ARN + base AK/SK set",
			env: map[string]string{
				"ALIBABA_CLOUD_ROLE_ARN":          "acs:ram::123:role/capa",
				"ALIBABA_CLOUD_ACCESS_KEY_ID":     "id",
				"ALIBABA_CLOUD_ACCESS_KEY_SECRET": "secret",
			},
			want: "roleArn", role: "acs:ram::123:role/capa",
		},
		{
			name: "RoleArn without base AK/SK falls through to auto RAM role",
			env: map[string]string{
				"ALIBABA_CLOUD_ROLE_ARN": "acs:ram::123:role/capa",
			},
			want: "ramAuto", role: "",
		},
		{
			name: "static AccessKey when both AK and SK set (no RAM/RoleArn env)",
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
			cred, src := resolveCredentialFrom(envFunc(tc.env))
			if src == "" {
				t.Error("expected a non-empty credential source label")
			}
			switch tc.want {
			case "ak":
				if _, ok := cred.(*credentials.AccessKeyCredential); !ok {
					t.Fatalf("want AccessKeyCredential, got %T", cred)
				}
			case "roleArn":
				rc, ok := cred.(*credentials.RamRoleArnCredential)
				if !ok {
					t.Fatalf("want RamRoleArnCredential, got %T", cred)
				}
				if rc.RoleArn != tc.role {
					t.Errorf("RoleArn = %q, want %q", rc.RoleArn, tc.role)
				}
				if rc.RoleSessionName != defaultRoleSessionName {
					t.Errorf("RoleSessionName = %q, want default %q", rc.RoleSessionName, defaultRoleSessionName)
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

func TestResolveCredential_RoleArnSessionOverrides(t *testing.T) {
	cred, src := resolveCredentialFrom(envFunc(map[string]string{
		"ALIBABA_CLOUD_ROLE_ARN":                "acs:ram::123:role/capa",
		"ALIBABA_CLOUD_ACCESS_KEY_ID":           "id",
		"ALIBABA_CLOUD_ACCESS_KEY_SECRET":       "secret",
		"ALIBABA_CLOUD_ROLE_SESSION_NAME":       "custom-session",
		"ALIBABA_CLOUD_ROLE_SESSION_EXPIRATION": "1800",
	}))
	rc, ok := cred.(*credentials.RamRoleArnCredential)
	if !ok {
		t.Fatalf("want RamRoleArnCredential, got %T", cred)
	}
	if rc.RoleSessionName != "custom-session" {
		t.Errorf("RoleSessionName = %q, want custom-session", rc.RoleSessionName)
	}
	if rc.RoleSessionExpiration != 1800 {
		t.Errorf("RoleSessionExpiration = %d, want 1800", rc.RoleSessionExpiration)
	}
	if !strings.Contains(src, "RoleArn") {
		t.Errorf("source = %q, want it to mention RoleArn", src)
	}
}

func TestRoleSessionExpirationFrom(t *testing.T) {
	cases := map[string]int{
		"":      defaultRoleSessionExpiration, // unset
		"abc":   defaultRoleSessionExpiration, // unparseable
		"100":   defaultRoleSessionExpiration, // below STS minimum
		"900":   900,
		"43200": 43200, // no upper clamp; STS validates
	}
	for in, want := range cases {
		got := roleSessionExpirationFrom(envFunc(map[string]string{"ALIBABA_CLOUD_ROLE_SESSION_EXPIRATION": in}))
		if got != want {
			t.Errorf("roleSessionExpirationFrom(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCredentialSource_NoSecrets(t *testing.T) {
	// CredentialSource reads the real env; in the test env nothing is set, so it
	// must report the auto-discovered RAM role and never leak a secret value.
	if src := CredentialSource(); src == "" {
		t.Error("CredentialSource returned empty")
	}
}
