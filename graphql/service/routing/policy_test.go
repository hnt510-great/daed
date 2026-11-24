package routing

import (
	"strings"
	"testing"
)

func TestDecomposeIPRangeV4AndV6(t *testing.T) {
	pfxs, err := DecomposeIPRange("192.168.1.10", "192.168.1.13")
	if err != nil {
		t.Fatalf("v4 decompose failed: %v", err)
	}
	// 10..11 -> /31, 12..13 -> /31
	if len(pfxs) != 2 || pfxs[0].String() != "192.168.1.10/31" || pfxs[1].String() != "192.168.1.12/31" {
		t.Fatalf("unexpected v4 cidrs: %v", pfxs)
	}

	pfxs6, err := DecomposeIPRange("2001:db8::10", "2001:db8::13")
	if err != nil {
		t.Fatalf("v6 decompose failed: %v", err)
	}
	if len(pfxs6) != 1 || pfxs6[0].String() != "2001:db8::10/126" {
		t.Fatalf("unexpected v6 cidrs: %v", pfxs6)
	}
}

func TestCompileVisualPolicyMixedSourcesAndUDP443Block(t *testing.T) {
	policy := &VisualPolicy{
		DefaultAction:   "group",
		DefaultGroup:    "proxy",
		SkipProxyPorts:  "udp:53",
		BlockProxyPorts: "udp:443",
		ACLs: []DeviceACLRule{
			{
				ID:      "direct-client",
				Name:    "Direct Client",
				Enabled: true,
				Sources: []ACLSource{
					{Kind: "mac", Value: "52:54:00:ed:02:02"},
					{Kind: "range", Value: "192.0.2.20-192.0.2.21"},
				},
				Action: "direct",
			},
			{
				ID:          "proxy-client",
				Name:        "Proxy Client",
				Enabled:     true,
				Sources:     []ACLSource{{Kind: "ip", Value: "192.0.2.10"}},
				Action:      "group",
				TargetGroup: "proxy",
				BlockProxyPorts: "udp:443",
			},
		},
	}

	compiled, err := CompileVisualPolicy(nil, policy)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(compiled.Diagnostics) > 0 {
		t.Fatalf("unexpected diagnostics: %+v", compiled.Diagnostics)
	}
	text := compiled.RoutingText
	// Mixed MAC + IP range expanded into two ordered branches for direct-client BEFORE proxy-client!
	idxMac := strings.Index(text, "mac('52:54:00:ed:02:02') -> direct")
	idxSip := strings.Index(text, "sip(192.0.2.20/31) -> direct")
	idxBlock := strings.Index(text, "sip(192.0.2.10/32) && l4proto(udp) && dport(443) -> block")
	idxProxy := strings.Index(text, "sip(192.0.2.10/32) -> proxy")
	if idxMac < 0 || idxSip < 0 || idxBlock < 0 || idxProxy < 0 {
		t.Fatalf("missing expected rules in compiled routing:\n%s", text)
	}
	if !(idxMac < idxSip && idxSip < idxBlock && idxBlock < idxProxy) {
		t.Fatalf("rule ordering wrong:\n%s", text)
	}
}
