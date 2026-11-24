package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/daeuniverse/dae-wing/db"
	_ "github.com/daeuniverse/dae/component/outbound"
)

func TestCandidateDeterministicDigestAndBackup(t *testing.T) {
	dir := t.TempDir()
	if err := db.InitDatabase(dir); err != nil {
		t.Fatalf("InitDatabase failed: %v", err)
	}
	d := db.DB(t.Context())

	// Seed basic config, dns, routing, node, group
	cfg := db.Config{Name: "default", Global: "global {\n  lan_interface: br-lan\n  dial_mode: ip\n}\n", Selected: true}
	if err := d.Create(&cfg).Error; err != nil {
		t.Fatal(err)
	}
	dn := db.Dns{Name: "default", Dns: "dns {\n  upstream {}\n  routing {\n    request { fallback: asis }\n    response { fallback: accept }\n  }\n}\n", Selected: true}
	if err := d.Create(&dn).Error; err != nil {
		t.Fatal(err)
	}
	rt := db.Routing{Name: "default", Routing: "routing {\n  dip(10.203.0.102/32) -> proxy\n  fallback: direct\n}\n", Selected: true, EditMode: db.RoutingEditModeRaw}
	if err := d.Create(&rt).Error; err != nil {
		t.Fatal(err)
	}
	n1 := db.Node{Link: "socks5://10.203.0.101:1080#n1", Name: "n1", Address: "10.203.0.101:1080", Protocol: "socks5"}
	if err := d.Create(&n1).Error; err != nil {
		t.Fatal(err)
	}
	grp := db.Group{Name: "proxy", Policy: "fixed", Node: []db.Node{n1}}
	if err := d.Create(&grp).Error; err != nil {
		t.Fatal(err)
	}

	c1, err := AssembleCandidate(d, AssembleOptions{ValidateDialers: true})
	if err != nil {
		t.Fatalf("AssembleCandidate 1 failed: %v", err)
	}
	if c1.HasErrors() {
		t.Fatalf("unexpected candidate errors: %+v", c1.Diagnostics)
	}
	c2, err := AssembleCandidate(d, AssembleOptions{ValidateDialers: true})
	if err != nil {
		t.Fatalf("AssembleCandidate 2 failed: %v", err)
	}
	if c1.Digest == "" || c1.Digest != c2.Digest {
		t.Fatalf("expected deterministic digest, got %q vs %q", c1.Digest, c2.Digest)
	}

	// Simulate NeedsRebind node -> must produce validation error
	if err := d.Model(&db.Node{ID: n1.ID}).Updates(map[string]interface{}{"needs_rebind": true, "rebind_reason": "removed"}).Error; err != nil {
		t.Fatal(err)
	}
	c3, err := AssembleCandidate(d, AssembleOptions{ValidateDialers: true})
	if err != nil {
		t.Fatalf("AssembleCandidate 3 failed: %v", err)
	}
	if !c3.HasErrors() {
		t.Fatalf("expected error when referenced node needs rebind")
	}

	// Verify visual policy with IPv6 CIDR + domain rule list + blockProxyPorts compiles & validates cleanly.
	if err := d.Model(&db.Node{ID: n1.ID}).Updates(map[string]interface{}{"needs_rebind": false, "rebind_reason": ""}).Error; err != nil {
		t.Fatal(err)
	}
	rlDom := db.RuleList{Name: "test-dom", Kind: "proxy", SourceType: "static", LastContent: "github.com\ngoogle.com\n", LastStatus: "ready"}
	rlIP := db.RuleList{Name: "test-ip", Kind: "proxy", SourceType: "static", LastContent: "1.1.1.1\n2001:67c:4e8::/48\n", LastStatus: "ready"}
	if err := d.Create(&rlDom).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&rlIP).Error; err != nil {
		t.Fatal(err)
	}
	vpJSON := `{"defaultAction":"group","defaultGroup":"proxy","blockProxyPorts":"udp:443","acls":[],"listRules":[{"id":"1","enabled":true,"listId":` +
		`1,"action":"group","targetGroup":"proxy"},{"id":"2","enabled":true,"listId":2,"action":"group","targetGroup":"proxy"}]}`
	if err := d.Model(&db.Routing{ID: rt.ID}).Updates(map[string]interface{}{"edit_mode": db.RoutingEditModeVisual, "visual_policy_json": vpJSON}).Error; err != nil {
		t.Fatal(err)
	}
	c4, err := AssembleCandidate(d, AssembleOptions{ValidateDialers: true})
	if err != nil {
		t.Fatalf("AssembleCandidate visual failed: %v", err)
	}
	if c4.HasErrors() {
		t.Fatalf("unexpected visual candidate errors: %+v\nrouting:\n%s", c4.Diagnostics, c4.RoutingText)
	}

	// Verify DB file permissions 0640
	fi, err := os.Stat(filepath.Join(dir, "wing.db"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0037 != 0 {
		t.Fatalf("wing.db permissions too open: %o", fi.Mode().Perm())
	}
}
