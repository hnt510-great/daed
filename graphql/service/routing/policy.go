/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package routing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/bits"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae-wing/graphql/service/rulelist"
	"gorm.io/gorm"
)

type ACLSource struct {
	Kind  string `json:"kind"` // mac | ip | cidr | range
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

type ACLDest struct {
	Kind   string `json:"kind"` // any | ip | cidr | range | domain | list
	Value  string `json:"value,omitempty"`
	ListID *uint  `json:"listId,omitempty"`
}

type DeviceACLRule struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Enabled         bool        `json:"enabled"`
	Sources         []ACLSource `json:"sources"`
	Destinations    []ACLDest   `json:"destinations,omitempty"`
	Protocol        string      `json:"protocol,omitempty"` // any | tcp | udp
	Ports           string      `json:"ports,omitempty"`    // e.g. "80,443,8000-8080"
	Action          string      `json:"action"`             // direct | block | group | inherit
	TargetGroup     string      `json:"targetGroup,omitempty"`
	SkipProxyPorts  string      `json:"skipProxyPorts,omitempty"`
	AllowProxyPorts string      `json:"allowProxyPorts,omitempty"`
	BlockProxyPorts string      `json:"blockProxyPorts,omitempty"`
}

type ListPolicyRule struct {
	ID          string `json:"id"`
	Enabled     bool   `json:"enabled"`
	ListID      uint   `json:"listId"`
	Action      string `json:"action"` // direct | block | group
	TargetGroup string `json:"targetGroup,omitempty"`
}

type VisualPolicy struct {
	DefaultAction   string           `json:"defaultAction"` // direct | block | group
	DefaultGroup    string           `json:"defaultGroup,omitempty"`
	SkipProxyPorts  string           `json:"skipProxyPorts,omitempty"`  // e.g. "tcp:22,udp:53" or "53,123"
	AllowProxyPorts string           `json:"allowProxyPorts,omitempty"` // if set, non-matching ports go direct
	BlockProxyPorts string           `json:"blockProxyPorts,omitempty"` // e.g. "udp:443" (only blocked for proxied traffic)
	ACLs            []DeviceACLRule  `json:"acls"`
	ListRules       []ListPolicyRule `json:"listRules"`
	ExtraRawRules   string           `json:"extraRawRules,omitempty"`
}

type PolicyDiagnostic struct {
	Field   string `json:"field"`
	Level   string `json:"level"` // error | warning
	Message string `json:"message"`
}

type CompiledPolicy struct {
	RoutingText string
	Lines       []string
	Diagnostics []PolicyDiagnostic
	ListIDs     []uint
}

type LANDeviceHint struct {
	Hostname  string `json:"hostname"`
	IP        string `json:"ip"`
	MAC       string `json:"mac"`
	Interface string `json:"interface"`
	Source    string `json:"source"` // dhcp | neigh
	Online    bool   `json:"online"`
}

// DiscoverLANDevices reads local DHCP leases and neighbor tables without
// depending on router-audit being enabled.
func DiscoverLANDevices() []LANDeviceHint {
	byKey := make(map[string]*LANDeviceHint)
	add := func(h LANDeviceHint) {
		h.MAC = strings.ToLower(strings.TrimSpace(h.MAC))
		h.IP = strings.TrimSpace(h.IP)
		if h.IP == "" && h.MAC == "" {
			return
		}
		key := h.MAC + "|" + h.IP
		if existing, ok := byKey[key]; ok {
			if existing.Hostname == "" && h.Hostname != "" {
				existing.Hostname = h.Hostname
			}
			if h.Online {
				existing.Online = true
			}
			if existing.Interface == "" && h.Interface != "" {
				existing.Interface = h.Interface
			}
			return
		}
		cp := h
		byKey[key] = &cp
	}

	// 1. /tmp/dhcp.leases: <expiry> <mac> <ip> <hostname> <clientid>
	if f, err := os.Open("/tmp/dhcp.leases"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) >= 4 {
				host := fields[3]
				if host == "*" {
					host = ""
				}
				add(LANDeviceHint{
					Hostname: host,
					MAC:      fields[1],
					IP:       fields[2],
					Source:   "dhcp",
					Online:   true,
				})
			}
		}
		_ = f.Close()
	}

	// 2. ip neigh show (covers both IPv4 and IPv6)
	if out, err := exec.Command("ip", "neigh", "show").Output(); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 4 {
				continue
			}
			ipStr := fields[0]
			var dev, mac, state string
			for i := 1; i < len(fields); i++ {
				switch fields[i] {
				case "dev":
					if i+1 < len(fields) {
						dev = fields[i+1]
						i++
					}
				case "lladdr":
					if i+1 < len(fields) {
						mac = fields[i+1]
						i++
					}
				}
			}
			state = fields[len(fields)-1]
			if mac != "" {
				add(LANDeviceHint{
					IP:        ipStr,
					MAC:       mac,
					Interface: dev,
					Source:    "neigh",
					Online:    state == "REACHABLE" || state == "STALE" || state == "DELAY" || state == "PROBE",
				})
			}
		}
	}

	res := make([]LANDeviceHint, 0, len(byKey))
	for _, h := range byKey {
		res = append(res, *h)
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].IP != res[j].IP {
			return res[i].IP < res[j].IP
		}
		return res[i].MAC < res[j].MAC
	})
	return res
}

// DecomposeIPRange converts an inclusive start..end IPv4 or IPv6 range into
// the minimal exact list of CIDR prefixes.
func DecomposeIPRange(startStr, endStr string) ([]netip.Prefix, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(startStr))
	if err != nil {
		return nil, fmt.Errorf("invalid range start IP %q: %w", startStr, err)
	}
	b, err := netip.ParseAddr(strings.TrimSpace(endStr))
	if err != nil {
		return nil, fmt.Errorf("invalid range end IP %q: %w", endStr, err)
	}
	a = a.Unmap()
	b = b.Unmap()
	if a.Is4() != b.Is4() {
		return nil, fmt.Errorf("IP range mixes IPv4 and IPv6: %s - %s", startStr, endStr)
	}
	if b.Less(a) {
		return nil, fmt.Errorf("IP range end %s is less than start %s", endStr, startStr)
	}
	if a.Is4() {
		return decomposeIPv4Range(a, b), nil
	}
	return decomposeIPv6Range(a, b), nil
}

func ipv4ToUint32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func uint32ToIPv4(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func decomposeIPv4Range(a, b netip.Addr) []netip.Prefix {
	start := uint64(ipv4ToUint32(a))
	end := uint64(ipv4ToUint32(b))
	var out []netip.Prefix
	for start <= end {
		maxAlign := uint64(32)
		if start != 0 {
			maxAlign = uint64(bits.TrailingZeros64(start))
		}
		rem := end - start + 1
		maxSize := uint64(63 - bits.LeadingZeros64(rem))
		shift := maxAlign
		if maxSize < shift {
			shift = maxSize
		}
		prefixLen := int(32 - shift)
		out = append(out, netip.PrefixFrom(uint32ToIPv4(uint32(start)), prefixLen))
		start += uint64(1) << shift
	}
	return out
}

type u128 struct{ hi, lo uint64 }

func addrToU128(a netip.Addr) u128 {
	b := a.As16()
	hi := uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
	lo := uint64(b[8])<<56 | uint64(b[9])<<48 | uint64(b[10])<<40 | uint64(b[11])<<32 |
		uint64(b[12])<<24 | uint64(b[13])<<16 | uint64(b[14])<<8 | uint64(b[15])
	return u128{hi, lo}
}

func u128ToAddr(v u128) netip.Addr {
	var b [16]byte
	for i := 0; i < 8; i++ {
		b[i] = byte(v.hi >> (56 - 8*i))
		b[8+i] = byte(v.lo >> (56 - 8*i))
	}
	return netip.AddrFrom16(b)
}

func (a u128) cmp(b u128) int {
	if a.hi != b.hi {
		if a.hi < b.hi {
			return -1
		}
		return 1
	}
	if a.lo != b.lo {
		if a.lo < b.lo {
			return -1
		}
		return 1
	}
	return 0
}

func (a u128) sub(b u128) u128 {
	lo := a.lo - b.lo
	borrow := uint64(0)
	if a.lo < b.lo {
		borrow = 1
	}
	return u128{hi: a.hi - b.hi - borrow, lo: lo}
}

func (a u128) addPow2(shift int) (u128, bool) {
	if shift >= 128 {
		return u128{}, true
	}
	if shift < 64 {
		inc := uint64(1) << shift
		lo := a.lo + inc
		carry := uint64(0)
		if lo < a.lo {
			carry = 1
		}
		hi := a.hi + carry
		return u128{hi, lo}, hi < a.hi
	}
	inc := uint64(1) << (shift - 64)
	hi := a.hi + inc
	return u128{hi, a.lo}, hi < a.hi
}

func (a u128) trailingZeros() int {
	if a.lo != 0 {
		return bits.TrailingZeros64(a.lo)
	}
	if a.hi != 0 {
		return 64 + bits.TrailingZeros64(a.hi)
	}
	return 128
}

func (a u128) bitLen() int {
	if a.hi != 0 {
		return 64 + bits.Len64(a.hi)
	}
	return bits.Len64(a.lo)
}

func decomposeIPv6Range(a, b netip.Addr) []netip.Prefix {
	start := addrToU128(a)
	end := addrToU128(b)
	var out []netip.Prefix
	for start.cmp(end) <= 0 {
		maxAlign := start.trailingZeros()
		diff := end.sub(start)
		// rem = diff + 1; find largest shift where (1<<shift) <= diff+1
		var maxSize int
		if diff.hi == ^uint64(0) && diff.lo == ^uint64(0) {
			maxSize = 128
		} else {
			rem, overflow := diff.addPow2(0)
			if overflow {
				maxSize = 128
			} else {
				maxSize = rem.bitLen() - 1
			}
		}
		shift := maxAlign
		if maxSize < shift {
			shift = maxSize
		}
		out = append(out, netip.PrefixFrom(u128ToAddr(start), 128-shift))
		next, overflow := start.addPow2(shift)
		if overflow {
			break
		}
		start = next
	}
	return out
}

type portSpec struct {
	proto string // "" | "tcp" | "udp"
	ports string // e.g. "443" or "8000-8080"
}

func parsePortSpecs(raw string) ([]portSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	tokens := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == ' '
	})
	var specs []portSpec
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		proto := ""
		portPart := tok
		if strings.HasPrefix(strings.ToLower(tok), "tcp:") {
			proto = "tcp"
			portPart = tok[4:]
		} else if strings.HasPrefix(strings.ToLower(tok), "udp:") {
			proto = "udp"
			portPart = tok[4:]
		}
		if err := validatePortExpr(portPart); err != nil {
			return nil, fmt.Errorf("invalid port spec %q: %w", tok, err)
		}
		specs = append(specs, portSpec{proto: proto, ports: portPart})
	}
	return specs, nil
}

func validatePortExpr(s string) error {
	if strings.Contains(s, "-") {
		parts := strings.Split(s, "-")
		if len(parts) != 2 {
			return fmt.Errorf("invalid range %q", s)
		}
		a, err1 := strconv.Atoi(parts[0])
		b, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || a < 1 || b > 65535 || a > b {
			return fmt.Errorf("port range out of bounds: %q", s)
		}
		return nil
	}
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("port out of bounds: %q", s)
	}
	return nil
}

func formatDaeIPArg(cidr string) string {
	if strings.Contains(cidr, ":") {
		return fmt.Sprintf("'%s'", cidr)
	}
	return cidr
}

func formatDaeDomainArg(cond string) string {
	// dae config_parser parses key:value params with the key outside quotes
	// (e.g. suffix:'example.com', full:'a.com', keyword:'foo', regexp:'^a').
	if idx := strings.Index(cond, ":"); idx > 0 {
		key := cond[:idx]
		val := strings.ReplaceAll(cond[idx+1:], "'", "\\'")
		if key == "domain" {
			key = "suffix"
		}
		return fmt.Sprintf("%s:'%s'", key, val)
	}
	val := strings.ReplaceAll(cond, "'", "\\'")
	return fmt.Sprintf("suffix:'%s'", val)
}

func formatAction(action, targetGroup string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "direct":
		return "direct", nil
	case "block":
		return "block", nil
	case "group":
		g := strings.TrimSpace(targetGroup)
		if g == "" {
			return "", fmt.Errorf("group action requires a target group")
		}
		return g, nil
	case "inherit":
		return "inherit", nil
	default:
		return "", fmt.Errorf("unsupported action %q", action)
	}
}

// CompileVisualPolicy deterministically compiles a VisualPolicy + referenced
// RuleLists into valid dae routing { ... } text and returns field diagnostics.
func CompileVisualPolicy(d *gorm.DB, policy *VisualPolicy) (*CompiledPolicy, error) {
	res := &CompiledPolicy{}
	if policy == nil {
		return res, nil
	}

	// Load all rule lists indexed by ID for deterministic lookup.
	var allLists []db.RuleList
	if d != nil {
		_ = d.Order("id asc").Find(&allLists).Error
	}
	listByID := make(map[uint]db.RuleList, len(allLists))
	for _, l := range allLists {
		listByID[l.ID] = l
	}
	usedLists := make(map[uint]struct{})

	defaultOut, err := formatAction(policy.DefaultAction, policy.DefaultGroup)
	if err != nil || defaultOut == "inherit" {
		if policy.DefaultAction == "" {
			defaultOut = "direct"
		} else {
			res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
				Field:   "defaultAction",
				Level:   "error",
				Message: fmt.Sprintf("invalid default action: %v", err),
			})
			defaultOut = "direct"
		}
	}

	var lines []string
	// Emit critical local/system bypass rules from extraRawRules at the very top
	// (e.g. pname(dnsmasq) -> must_direct, dip(geoip:private) -> direct) so they
	// are never shadowed by proxy IP/domain lists.
	var topExtraLines, bottomExtraLines []string
	if extra := strings.TrimSpace(policy.ExtraRawRules); extra != "" {
		for _, l := range strings.Split(strings.ReplaceAll(extra, "\r\n", "\n"), "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			lower := strings.ToLower(l)
			if strings.Contains(lower, "must_direct") || strings.Contains(lower, "geoip:private") ||
				(strings.HasPrefix(lower, "dip(") && strings.HasSuffix(lower, "-> direct") && !strings.Contains(lower, "geoip:cn")) {
				topExtraLines = append(topExtraLines, l)
			} else {
				bottomExtraLines = append(bottomExtraLines, l)
			}
		}
	}
	lines = append(lines, topExtraLines...)

	// Helper to emit port filter rules for a given source prefix (empty for global).
	emitPortRule := func(srcPrefix []string, spec portSpec, outbound string) {
		conds := append([]string{}, srcPrefix...)
		if spec.proto != "" {
			conds = append(conds, fmt.Sprintf("l4proto(%s)", spec.proto))
		}
		conds = append(conds, fmt.Sprintf("dport(%s)", spec.ports))
		lines = append(lines, fmt.Sprintf("%s -> %s", strings.Join(conds, " && "), outbound))
	}

	// Helper to emit allow-proxy-ports (non-matching ports go direct).
	emitAllowProxyPorts := func(srcPrefix []string, specs []portSpec) {
		if len(specs) == 0 {
			return
		}
		var tcpPorts, udpPorts, anyPorts []string
		for _, s := range specs {
			switch s.proto {
			case "tcp":
				tcpPorts = append(tcpPorts, s.ports)
			case "udp":
				udpPorts = append(udpPorts, s.ports)
			default:
				anyPorts = append(anyPorts, s.ports)
			}
		}
		if len(anyPorts) > 0 && len(tcpPorts) == 0 && len(udpPorts) == 0 {
			conds := append([]string{}, srcPrefix...)
			conds = append(conds, fmt.Sprintf("!dport(%s)", strings.Join(anyPorts, ",")))
			lines = append(lines, fmt.Sprintf("%s -> direct", strings.Join(conds, " && ")))
			return
		}
		if len(tcpPorts) > 0 || len(anyPorts) > 0 {
			all := append(append([]string{}, tcpPorts...), anyPorts...)
			conds := append([]string{}, srcPrefix...)
			conds = append(conds, "l4proto(tcp)", fmt.Sprintf("!dport(%s)", strings.Join(all, ",")))
			lines = append(lines, fmt.Sprintf("%s -> direct", strings.Join(conds, " && ")))
		}
		if len(udpPorts) > 0 || len(anyPorts) > 0 {
			all := append(append([]string{}, udpPorts...), anyPorts...)
			conds := append([]string{}, srcPrefix...)
			conds = append(conds, "l4proto(udp)", fmt.Sprintf("!dport(%s)", strings.Join(all, ",")))
			lines = append(lines, fmt.Sprintf("%s -> direct", strings.Join(conds, " && ")))
		}
	}

	// 1. Ordered Device ACL rules.
	for i, acl := range policy.ACLs {
		if !acl.Enabled {
			continue
		}
		fieldPrefix := fmt.Sprintf("acls[%d]", i)
		out, err := formatAction(acl.Action, acl.TargetGroup)
		if err != nil {
			res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
				Field:   fieldPrefix + ".action",
				Level:   "error",
				Message: err.Error(),
			})
			continue
		}

		// Split sources into MAC branch and IP/CIDR branch so mixed MAC+IP
		// sources expand into equivalent branches preserving rule order.
		var macs []string
		var sipCIDRs []string
		for si, src := range acl.Sources {
			v := strings.TrimSpace(src.Value)
			if v == "" {
				continue
			}
			switch strings.ToLower(src.Kind) {
			case "mac":
				if _, err := net.ParseMAC(v); err != nil {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.sources[%d]", fieldPrefix, si),
						Level:   "error",
						Message: fmt.Sprintf("invalid MAC address %q", v),
					})
					continue
				}
				macs = append(macs, fmt.Sprintf("'%s'", strings.ToLower(v)))
			case "ip":
				addr, err := netip.ParseAddr(v)
				if err != nil {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.sources[%d]", fieldPrefix, si),
						Level:   "error",
						Message: fmt.Sprintf("invalid source IP %q", v),
					})
					continue
				}
				bitsLen := 32
				if addr.Is6() {
					bitsLen = 128
				}
				sipCIDRs = append(sipCIDRs, formatDaeIPArg(netip.PrefixFrom(addr.Unmap(), bitsLen).String()))
			case "cidr":
				pfx, err := netip.ParsePrefix(v)
				if err != nil {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.sources[%d]", fieldPrefix, si),
						Level:   "error",
						Message: fmt.Sprintf("invalid source CIDR %q", v),
					})
					continue
				}
				sipCIDRs = append(sipCIDRs, formatDaeIPArg(pfx.Masked().String()))
			case "range":
				parts := strings.Split(v, "-")
				if len(parts) != 2 {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.sources[%d]", fieldPrefix, si),
						Level:   "error",
						Message: fmt.Sprintf("invalid source IP range %q (expected start-end)", v),
					})
					continue
				}
				pfxs, err := DecomposeIPRange(parts[0], parts[1])
				if err != nil {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.sources[%d]", fieldPrefix, si),
						Level:   "error",
						Message: err.Error(),
					})
					continue
				}
				for _, p := range pfxs {
					sipCIDRs = append(sipCIDRs, formatDaeIPArg(p.String()))
				}
			default:
				res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
					Field:   fmt.Sprintf("%s.sources[%d]", fieldPrefix, si),
					Level:   "error",
					Message: fmt.Sprintf("unsupported source kind %q", src.Kind),
				})
			}
		}

		// Build source branches (OR within sources).
		var srcBranches [][]string
		if len(macs) > 0 {
			srcBranches = append(srcBranches, []string{fmt.Sprintf("mac(%s)", strings.Join(macs, ", "))})
		}
		if len(sipCIDRs) > 0 {
			srcBranches = append(srcBranches, []string{fmt.Sprintf("sip(%s)", strings.Join(sipCIDRs, ", "))})
		}
		if len(srcBranches) == 0 {
			// Applies to any source.
			srcBranches = [][]string{{}}
		}

		// Build destination branches.
		var dipCIDRs []string
		var domains []string
		for di, dst := range acl.Destinations {
			switch strings.ToLower(dst.Kind) {
			case "", "any":
				continue
			case "ip":
				addr, err := netip.ParseAddr(strings.TrimSpace(dst.Value))
				if err != nil {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.destinations[%d]", fieldPrefix, di),
						Level:   "error",
						Message: fmt.Sprintf("invalid destination IP %q", dst.Value),
					})
					continue
				}
				bitsLen := 32
				if addr.Is6() {
					bitsLen = 128
				}
				dipCIDRs = append(dipCIDRs, formatDaeIPArg(netip.PrefixFrom(addr.Unmap(), bitsLen).String()))
			case "cidr":
				pfx, err := netip.ParsePrefix(strings.TrimSpace(dst.Value))
				if err != nil {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.destinations[%d]", fieldPrefix, di),
						Level:   "error",
						Message: fmt.Sprintf("invalid destination CIDR %q", dst.Value),
					})
					continue
				}
				dipCIDRs = append(dipCIDRs, formatDaeIPArg(pfx.Masked().String()))
			case "range":
				parts := strings.Split(strings.TrimSpace(dst.Value), "-")
				if len(parts) != 2 {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.destinations[%d]", fieldPrefix, di),
						Level:   "error",
						Message: fmt.Sprintf("invalid destination IP range %q", dst.Value),
					})
					continue
				}
				pfxs, err := DecomposeIPRange(parts[0], parts[1])
				if err != nil {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.destinations[%d]", fieldPrefix, di),
						Level:   "error",
						Message: err.Error(),
					})
					continue
				}
				for _, p := range pfxs {
					dipCIDRs = append(dipCIDRs, formatDaeIPArg(p.String()))
				}
			case "domain":
				v := strings.TrimSpace(dst.Value)
				if v != "" {
					domains = append(domains, formatDaeDomainArg(v))
				}
			case "list":
				if dst.ListID == nil {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.destinations[%d]", fieldPrefix, di),
						Level:   "error",
						Message: "list destination requires listId",
					})
					continue
				}
				rl, ok := listByID[*dst.ListID]
				if !ok {
					res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
						Field:   fmt.Sprintf("%s.destinations[%d]", fieldPrefix, di),
						Level:   "error",
						Message: fmt.Sprintf("referenced rule list ID %d not found", *dst.ListID),
					})
					continue
				}
				usedLists[rl.ID] = struct{}{}
				parsed := rulelist.ParseRuleList(rl.LastContent, rl.Kind)
				for _, e := range parsed.Entries {
					if e.Kind == rulelist.EntryIPv4CIDR || e.Kind == rulelist.EntryIPv6CIDR {
						dipCIDRs = append(dipCIDRs, formatDaeIPArg(e.Condition))
					} else {
						domains = append(domains, formatDaeDomainArg(e.Condition))
					}
				}
			}
		}

		// Protocol & port constraints on the ACL rule itself.
		var protoConds []string
		proto := strings.ToLower(strings.TrimSpace(acl.Protocol))
		if proto == "tcp" || proto == "udp" {
			protoConds = append(protoConds, fmt.Sprintf("l4proto(%s)", proto))
		}
		if strings.TrimSpace(acl.Ports) != "" {
			specs, err := parsePortSpecs(acl.Ports)
			if err != nil {
				res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
					Field:   fieldPrefix + ".ports",
					Level:   "error",
					Message: err.Error(),
				})
				continue
			}
			var ps []string
			for _, s := range specs {
				ps = append(ps, s.ports)
			}
			if len(ps) > 0 {
				protoConds = append(protoConds, fmt.Sprintf("dport(%s)", strings.Join(ps, ",")))
			}
		}

		// Per-ACL skip/allow/block proxy ports (only meaningful when action is a proxy group).
		if out != "direct" && out != "block" && out != "inherit" {
			if specs, err := parsePortSpecs(acl.SkipProxyPorts); err == nil {
				for _, s := range specs {
					for _, sb := range srcBranches {
						emitPortRule(sb, s, "direct")
					}
				}
			} else {
				res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{Field: fieldPrefix + ".skipProxyPorts", Level: "error", Message: err.Error()})
			}
			if specs, err := parsePortSpecs(acl.AllowProxyPorts); err == nil && len(specs) > 0 {
				for _, sb := range srcBranches {
					emitAllowProxyPorts(sb, specs)
				}
			} else if err != nil {
				res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{Field: fieldPrefix + ".allowProxyPorts", Level: "error", Message: err.Error()})
			}
			if specs, err := parsePortSpecs(acl.BlockProxyPorts); err == nil {
				for _, s := range specs {
					for _, sb := range srcBranches {
						emitPortRule(sb, s, "block")
					}
				}
			} else {
				res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{Field: fieldPrefix + ".blockProxyPorts", Level: "error", Message: err.Error()})
			}
		}

		// If action == "inherit", skip emitting a catch-all terminal rule for this ACL
		// so traffic falls through to global list rules & default action.
		if out == "inherit" {
			continue
		}

		// Emit destination-qualified or unqualified rules for each source branch.
		var dstBranches [][]string
		if len(dipCIDRs) > 0 {
			dstBranches = append(dstBranches, []string{fmt.Sprintf("dip(%s)", strings.Join(dipCIDRs, ", "))})
		}
		if len(domains) > 0 {
			dstBranches = append(dstBranches, []string{fmt.Sprintf("domain(%s)", strings.Join(domains, ", "))})
		}
		if len(dstBranches) == 0 {
			dstBranches = [][]string{{}}
		}

		for _, sb := range srcBranches {
			for _, db := range dstBranches {
				conds := make([]string, 0, len(sb)+len(db)+len(protoConds))
				conds = append(conds, sb...)
				conds = append(conds, db...)
				conds = append(conds, protoConds...)
				if len(conds) == 0 {
					continue
				}
				lines = append(lines, fmt.Sprintf("%s -> %s", strings.Join(conds, " && "), out))
			}
		}
	}

	// 2. Global skipProxyPorts & allowProxyPorts (always before proxy list/default rules).
	if specs, err := parsePortSpecs(policy.SkipProxyPorts); err == nil {
		for _, s := range specs {
			emitPortRule(nil, s, "direct")
		}
	} else {
		res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{Field: "skipProxyPorts", Level: "error", Message: err.Error()})
	}
	if specs, err := parsePortSpecs(policy.AllowProxyPorts); err == nil && len(specs) > 0 {
		emitAllowProxyPorts(nil, specs)
	} else if err != nil {
		res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{Field: "allowProxyPorts", Level: "error", Message: err.Error()})
	}

	// Parse global blockProxyPorts (applied ONLY to traffic that would be proxied,
	// i.e., before proxy list rules and before a proxy defaultAction, NEVER blocking direct traffic).
	blockProxySpecs, err := parsePortSpecs(policy.BlockProxyPorts)
	if err != nil {
		res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{Field: "blockProxyPorts", Level: "error", Message: err.Error()})
	}

	// 3. Ordered RuleList policy rules.
	for i, lr := range policy.ListRules {
		if !lr.Enabled {
			continue
		}
		fieldPrefix := fmt.Sprintf("listRules[%d]", i)
		rl, ok := listByID[lr.ListID]
		if !ok {
			res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
				Field:   fieldPrefix + ".listId",
				Level:   "error",
				Message: fmt.Sprintf("rule list ID %d not found", lr.ListID),
			})
			continue
		}
		usedLists[rl.ID] = struct{}{}
		out, err := formatAction(lr.Action, lr.TargetGroup)
		if err != nil || out == "inherit" {
			res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
				Field:   fieldPrefix + ".action",
				Level:   "error",
				Message: fmt.Sprintf("invalid list rule action: %v", err),
			})
			continue
		}
		parsed := rulelist.ParseRuleList(rl.LastContent, rl.Kind)
		if parsed.HasErrors {
			for _, diag := range parsed.Diagnostics {
				res.Diagnostics = append(res.Diagnostics, PolicyDiagnostic{
					Field:   fieldPrefix,
					Level:   "error",
					Message: fmt.Sprintf("rule list %q: %s", rl.Name, diag.Message),
				})
			}
			continue
		}
		// Exceptions take precedence over the list entries (go direct when list is proxy, etc.)
		if len(parsed.Exceptions) > 0 {
			var excDom, excIP []string
			for _, e := range parsed.Exceptions {
				if e.Kind == rulelist.EntryIPv4CIDR || e.Kind == rulelist.EntryIPv6CIDR {
					excIP = append(excIP, formatDaeIPArg(e.Condition))
				} else {
					excDom = append(excDom, formatDaeDomainArg(e.Condition))
				}
			}
			excOut := "direct"
			if out == "direct" {
				excOut = defaultOut
			}
			if len(excDom) > 0 {
				lines = append(lines, fmt.Sprintf("domain(%s) -> %s", strings.Join(excDom, ", "), excOut))
			}
			if len(excIP) > 0 {
				lines = append(lines, fmt.Sprintf("dip(%s) -> %s", strings.Join(excIP, ", "), excOut))
			}
		}

		var doms, ips []string
		for _, e := range parsed.Entries {
			if e.Kind == rulelist.EntryIPv4CIDR || e.Kind == rulelist.EntryIPv6CIDR {
				ips = append(ips, formatDaeIPArg(e.Condition))
			} else {
				doms = append(doms, formatDaeDomainArg(e.Condition))
			}
		}
		// If this list routes to a proxy group and blockProxyPorts is configured,
		// block those ports on matching destinations BEFORE the proxy rule.
		if out != "direct" && out != "block" && len(blockProxySpecs) > 0 {
			for _, spec := range blockProxySpecs {
				if len(doms) > 0 {
					conds := []string{fmt.Sprintf("domain(%s)", strings.Join(doms, ", "))}
					if spec.proto != "" {
						conds = append(conds, fmt.Sprintf("l4proto(%s)", spec.proto))
					}
					conds = append(conds, fmt.Sprintf("dport(%s)", spec.ports))
					lines = append(lines, fmt.Sprintf("%s -> block", strings.Join(conds, " && ")))
				}
				if len(ips) > 0 {
					conds := []string{fmt.Sprintf("dip(%s)", strings.Join(ips, ", "))}
					if spec.proto != "" {
						conds = append(conds, fmt.Sprintf("l4proto(%s)", spec.proto))
					}
					conds = append(conds, fmt.Sprintf("dport(%s)", spec.ports))
					lines = append(lines, fmt.Sprintf("%s -> block", strings.Join(conds, " && ")))
				}
			}
		}
		if len(doms) > 0 {
			lines = append(lines, fmt.Sprintf("domain(%s) -> %s", strings.Join(doms, ", "), out))
		}
		if len(ips) > 0 {
			lines = append(lines, fmt.Sprintf("dip(%s) -> %s", strings.Join(ips, ", "), out))
		}
	}

	// 4. Remaining geo/fallback extra raw rules (geoip:cn, geosite:cn, geosite:gfw, etc.).
	lines = append(lines, bottomExtraLines...)

	// 5. Global blockProxyPorts before default fallback if default fallback is a proxy group.
	if defaultOut != "direct" && defaultOut != "block" && len(blockProxySpecs) > 0 {
		for _, spec := range blockProxySpecs {
			emitPortRule(nil, spec, "block")
		}
	}

	lines = append(lines, fmt.Sprintf("fallback: %s", defaultOut))

	var sb strings.Builder
	sb.WriteString("routing {\n")
	for _, l := range lines {
		sb.WriteString("  ")
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	sb.WriteString("}\n")

	res.Lines = lines
	res.RoutingText = sb.String()
	for id := range usedLists {
		res.ListIDs = append(res.ListIDs, id)
	}
	sort.Slice(res.ListIDs, func(i, j int) bool { return res.ListIDs[i] < res.ListIDs[j] })
	return res, nil
}

// SavePolicyDraft persists a visual policy draft without triggering a reload.
func SavePolicyDraft(ctx context.Context, routingID uint, expectedRevision *uint, policy VisualPolicy) (*db.Routing, []PolicyDiagnostic, error) {
	tx := db.BeginTx(ctx)
	defer tx.Rollback()

	var m db.Routing
	if err := tx.Where("id = ?", routingID).First(&m).Error; err != nil {
		return nil, nil, err
	}
	if expectedRevision != nil && m.DraftRevision != *expectedRevision {
		return nil, nil, fmt.Errorf("draft revision conflict: routing %q is at revision %d, expected %d", m.Name, m.DraftRevision, *expectedRevision)
	}
	rawJSON, err := json.Marshal(policy)
	if err != nil {
		return nil, nil, err
	}
	compiled, err := CompileVisualPolicy(tx, &policy)
	if err != nil {
		return nil, nil, err
	}

	m.EditMode = db.RoutingEditModeVisual
	m.VisualPolicyJSON = string(rawJSON)
	m.GeneratedRouting = compiled.RoutingText
	m.DraftRevision++
	m.Version++
	if err := tx.Save(&m).Error; err != nil {
		return nil, nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, nil, err
	}
	return &m, compiled.Diagnostics, nil
}

// SetEditMode switches a routing between "visual" and "raw" without losing either representation.
func SetEditMode(ctx context.Context, routingID uint, editMode string) (*db.Routing, error) {
	mode := strings.ToLower(strings.TrimSpace(editMode))
	if mode != db.RoutingEditModeRaw && mode != db.RoutingEditModeVisual {
		return nil, fmt.Errorf("invalid edit mode %q (expected 'raw' or 'visual')", editMode)
	}
	tx := db.BeginTx(ctx)
	defer tx.Rollback()
	var m db.Routing
	if err := tx.Where("id = ?", routingID).First(&m).Error; err != nil {
		return nil, err
	}
	m.EditMode = mode
	m.Version++
	if err := tx.Save(&m).Error; err != nil {
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// CloneAsRawCopy clones a visual routing's generated text into a new raw routing.
func CloneAsRawCopy(ctx context.Context, routingID uint, newName string) (*db.Routing, error) {
	var src db.Routing
	if err := db.DB(ctx).Where("id = ?", routingID).First(&src).Error; err != nil {
		return nil, err
	}
	text := src.Routing
	if src.EditMode == db.RoutingEditModeVisual && strings.TrimSpace(src.GeneratedRouting) != "" {
		text = src.GeneratedRouting
	}
	name := strings.TrimSpace(newName)
	if name == "" {
		name = src.Name + " (raw)"
	}
	copyModel := db.Routing{
		Name:     name,
		Routing:  text,
		EditMode: db.RoutingEditModeRaw,
	}
	if err := db.DB(ctx).Create(&copyModel).Error; err != nil {
		return nil, err
	}
	return &copyModel, nil
}
