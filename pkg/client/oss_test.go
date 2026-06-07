package client

import "testing"

func TestOSSEndpointFor(t *testing.T) {
	if got := ossEndpointFor("", "cn-wulanchabu"); got != "https://oss-cn-wulanchabu-internal.aliyuncs.com" {
		t.Errorf("default endpoint = %q", got)
	}
	if got := ossEndpointFor("https://custom.example.com", "cn-wulanchabu"); got != "https://custom.example.com" {
		t.Errorf("override endpoint = %q", got)
	}
}
