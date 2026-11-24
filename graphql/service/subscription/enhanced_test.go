package subscription

import (
	"net/http"
	"testing"
)

func TestClashYAMLAndUserinfoAndFilters(t *testing.T) {
	yamlBody := `
proxies:
  - name: "HK-1"
    type: socks5
    server: 10.203.0.101
    port: 1080
  - name: "Bad-XHTTP"
    type: vless
    server: 1.2.3.4
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
    network: xhttp
  - name: "Bad-WG"
    type: wireguard
    server: 1.2.3.4
    port: 51820
`
	links, errs, fmtName, fatal := ParseSubscriptionPayload(yamlBody, "auto")
	if fatal != nil {
		t.Fatalf("unexpected fatal error: %v", fatal)
	}
	if fmtName != "clash_yaml" {
		t.Fatalf("expected clash_yaml, got %s", fmtName)
	}
	if len(links) != 1 || len(errs) != 2 {
		t.Fatalf("expected 1 valid link and 2 errors, got links=%d errs=%+v", len(links), errs)
	}
	if errs[0].Param != "network" || errs[1].Protocol != "wireguard" {
		t.Fatalf("unexpected error details: %+v", errs)
	}

	hdr := http.Header{}
	hdr.Set("Subscription-Userinfo", "upload=1024; download=2048; total=1048576; expire=1800000000")
	ui := ParseSubscriptionUserinfo(hdr)
	if ui.Upload != 1024 || ui.Download != 2048 || ui.Total != 1048576 || ui.ExpireAt == nil {
		t.Fatalf("unexpected userinfo: %+v", ui)
	}

	// Keyword filter priority
	if matchesKeywordFilter("HK-Trial", "HK", "Trial", "exclude_first") {
		t.Fatalf("exclude_first should exclude HK-Trial")
	}
	if !matchesKeywordFilter("HK-Trial", "HK", "Trial", "include_first") {
		t.Fatalf("include_first should keep HK-Trial")
	}

	// StableKey ignores remark, IdentityKey ignores password
	sk1, ik1 := ComputeNodeKeys("socks5://user:pass1@10.0.0.1:1080#NameA")
	sk2, ik2 := ComputeNodeKeys("socks5://user:pass1@10.0.0.1:1080#NameB")
	sk3, ik3 := ComputeNodeKeys("socks5://user:pass2@10.0.0.1:1080#NameB")
	if sk1 != sk2 {
		t.Fatalf("rename should preserve StableKey")
	}
	if sk1 == sk3 {
		t.Fatalf("credential change should change StableKey")
	}
	if ik1 != ik3 || ik2 != ik3 {
		t.Fatalf("credential change on same endpoint should preserve IdentityKey")
	}
}
