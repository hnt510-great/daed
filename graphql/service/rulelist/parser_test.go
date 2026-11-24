package rulelist

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseRuleListFormatsAndExceptions(t *testing.T) {
	raw := strings.Join([]string{
		"# comment",
		"example.com",
		"full:exact.example.org",
		"keyword:tracker",
		"regexp:^ads\\..*\\.net$",
		"10.0.0.0/8",
		"2001:db8::/32",
		"server=/cn.example.com/223.5.5.5",
		"||blocked.example.com",
		"@@||allowed.example.com",
	}, "\n")

	res := ParseRuleList(raw, "domain")
	if res.HasErrors {
		t.Fatalf("unexpected parse errors: %+v", res.Diagnostics)
	}
	if len(res.Entries) != 8 {
		t.Fatalf("expected 8 entries, got %d: %+v", len(res.Entries), res.Entries)
	}
	if len(res.Exceptions) != 1 || res.Exceptions[0].Value != "allowed.example.com" {
		t.Fatalf("expected 1 exception for allowed.example.com, got %+v", res.Exceptions)
	}

	// Base64 GFWList
	gfw := "[AutoProxy 0.2.9]\n||google.com\n.youtube.com\n"
	b64 := base64.StdEncoding.EncodeToString([]byte(gfw))
	resB64 := ParseRuleList(b64, "gfw")
	if resB64.HasErrors || len(resB64.Entries) != 2 {
		t.Fatalf("expected 2 entries from base64 gfwlist, got %+v (diags=%+v)", resB64.Entries, resB64.Diagnostics)
	}

	// Unsupported regex exception reports line number
	badExc := "example.com\n@@/foo.*bar/\n"
	resBad := ParseRuleList(badExc, "gfw")
	if !resBad.HasErrors || len(resBad.Diagnostics) == 0 || resBad.Diagnostics[0].Line != 2 {
		t.Fatalf("expected error at line 2 for unsupported exception, got %+v", resBad.Diagnostics)
	}

	// HTML error page rejected
	htmlRes := ParseRuleList("<!DOCTYPE html><html><title>502 Bad Gateway</title></html>", "domain")
	if !htmlRes.HasErrors {
		t.Fatalf("expected HTML error page to be rejected")
	}

	// Diff counts
	added, removed := DiffEntryCounts("a.com\nb.com\n", "b.com\nc.com\nd.com\n", "domain")
	if added != 2 || removed != 1 {
		t.Fatalf("expected added=2 removed=1, got added=%d removed=%d", added, removed)
	}
}
