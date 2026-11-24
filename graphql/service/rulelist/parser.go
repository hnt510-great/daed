/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package rulelist

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

type EntryKind string

const (
	EntryDomainFull    EntryKind = "full"
	EntryDomainSuffix  EntryKind = "domain"
	EntryDomainKeyword EntryKind = "keyword"
	EntryDomainRegexp  EntryKind = "regexp"
	EntryIPv4CIDR      EntryKind = "ipv4_cidr"
	EntryIPv6CIDR      EntryKind = "ipv6_cidr"
)

type ParsedEntry struct {
	Kind      EntryKind `json:"kind"`
	Value     string    `json:"value"`
	Negated   bool      `json:"negated,omitempty"`
	Line      int       `json:"line"`
	Condition string    `json:"condition"` // dae matcher arg, e.g. "domain:example.com" or "1.2.3.0/24"
}

type ParseDiagnostic struct {
	Line    int    `json:"line"`
	Level   string `json:"level"` // error | warning
	Message string `json:"message"`
}

type ParseResult struct {
	Entries     []ParsedEntry     `json:"entries"`
	Exceptions  []ParsedEntry     `json:"exceptions"`
	Diagnostics []ParseDiagnostic `json:"diagnostics"`
	HasErrors   bool              `json:"hasErrors"`
}

var (
	reHTMLSniff = regexp.MustCompile(`(?i)^\s*(<!doctype\s+html|<html[\s>]|<head[\s>]|<body[\s>]|<title[\s>])`)
	reDomain    = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)
)

// IsLikelyHTML returns true when raw response bytes look like an HTML error page.
func IsLikelyHTML(raw string) bool {
	head := raw
	if len(head) > 512 {
		head = head[:512]
	}
	return reHTMLSniff.MatchString(head)
}

// maybeDecodeGFWListBase64 decodes base64-encoded GFWList payloads when applicable.
func maybeDecodeGFWListBase64(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.Contains(trimmed, " ") || strings.Contains(trimmed, "://") ||
		strings.HasPrefix(trimmed, "[AutoProxy") || strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "!") || strings.Contains(trimmed, "server=/") {
		return raw
	}
	// Strip whitespace and try base64 decode.
	compact := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' {
			return -1
		}
		return r
	}, trimmed)
	if len(compact) < 16 {
		return raw
	}
	decoded, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(compact)
		if err != nil {
			return raw
		}
	}
	s := string(decoded)
	if strings.Contains(s, "[AutoProxy") || strings.Contains(s, "||") || strings.Contains(s, "\n") {
		return s
	}
	return raw
}

// ParseRuleList parses plain domain/CIDR lines, GFWList (plain or base64),
// and dnsmasq domain entries while preserving entry order, match kinds,
// and exception line diagnostics.
func ParseRuleList(raw string, kind string) *ParseResult {
	res := &ParseResult{}
	if strings.TrimSpace(raw) == "" {
		return res
	}
	if IsLikelyHTML(raw) {
		res.HasErrors = true
		res.Diagnostics = append(res.Diagnostics, ParseDiagnostic{
			Line:    1,
			Level:   "error",
			Message: "response appears to be an HTML error page rather than a rule list",
		})
		return res
	}

	text := maybeDecodeGFWListBase64(raw)
	seen := make(map[string]struct{})
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	for idx, orig := range lines {
		lineNum := idx + 1
		line := strings.TrimSpace(orig)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") ||
			strings.HasPrefix(line, "[AutoProxy") || strings.HasPrefix(line, "//") {
			continue
		}
		// Strip inline comments for plain text lines (not regex/URLs).
		if !strings.HasPrefix(line, "/") && !strings.Contains(line, "://") {
			if ci := strings.Index(line, " #"); ci > 0 {
				line = strings.TrimSpace(line[:ci])
			}
		}

		// Handle GFWList / rule exception syntax: @@...
		if strings.HasPrefix(line, "@@") {
			excBody := strings.TrimSpace(strings.TrimPrefix(line, "@@"))
			entry, err := parseSingleLine(excBody, lineNum, kind)
			if err != nil || entry == nil {
				res.HasErrors = true
				res.Diagnostics = append(res.Diagnostics, ParseDiagnostic{
					Line:    lineNum,
					Level:   "error",
					Message: fmt.Sprintf("unsupported exception at line %d: %q (%v)", lineNum, orig, err),
				})
				continue
			}
			// Regex or path-based exceptions cannot be reliably inverted in dae routing.
			if entry.Kind == EntryDomainRegexp || strings.Contains(excBody, "/") {
				res.HasErrors = true
				res.Diagnostics = append(res.Diagnostics, ParseDiagnostic{
					Line:    lineNum,
					Level:   "error",
					Message: fmt.Sprintf("exception at line %d cannot be safely expressed in dae routing: %q", lineNum, orig),
				})
				continue
			}
			entry.Negated = true
			key := "exc:" + string(entry.Kind) + ":" + entry.Value
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				res.Exceptions = append(res.Exceptions, *entry)
			}
			continue
		}

		entry, err := parseSingleLine(line, lineNum, kind)
		if err != nil {
			res.HasErrors = true
			res.Diagnostics = append(res.Diagnostics, ParseDiagnostic{
				Line:    lineNum,
				Level:   "error",
				Message: fmt.Sprintf("invalid rule entry at line %d: %v", lineNum, err),
			})
			continue
		}
		if entry == nil {
			continue
		}
		key := string(entry.Kind) + ":" + entry.Value
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		res.Entries = append(res.Entries, *entry)
	}
	return res
}

func parseSingleLine(line string, lineNum int, listKind string) (*ParsedEntry, error) {
	// 1. dnsmasq syntax: server=/example.com/1.1.1.1, address=/example.com/#, ipset=/example.com/set, nftset=/example.com/...
	for _, prefix := range []string{"server=/", "address=/", "ipset=/", "nftset=/", "local=/"} {
		if strings.HasPrefix(line, prefix) {
			rest := strings.TrimPrefix(line, prefix)
			parts := strings.Split(rest, "/")
			if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
				return nil, fmt.Errorf("malformed dnsmasq entry %q", line)
			}
			dom := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(parts[0]), "."))
			if !isValidDomain(dom) {
				return nil, fmt.Errorf("invalid dnsmasq domain %q", dom)
			}
			return &ParsedEntry{
				Kind:      EntryDomainSuffix,
				Value:     dom,
				Line:      lineNum,
				Condition: "domain:" + dom,
			}, nil
		}
	}

	// 2. Explicit dae prefixes: full:, domain:, suffix:, keyword:, regexp:
	if strings.HasPrefix(line, "full:") {
		v := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "full:")))
		if !isValidDomain(v) {
			return nil, fmt.Errorf("invalid full domain %q", v)
		}
		return &ParsedEntry{Kind: EntryDomainFull, Value: v, Line: lineNum, Condition: "full:" + v}, nil
	}
	if strings.HasPrefix(line, "domain:") || strings.HasPrefix(line, "suffix:") {
		v := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "domain:"), "suffix:")))
		v = strings.TrimPrefix(v, ".")
		if !isValidDomain(v) {
			return nil, fmt.Errorf("invalid suffix domain %q", v)
		}
		return &ParsedEntry{Kind: EntryDomainSuffix, Value: v, Line: lineNum, Condition: "domain:" + v}, nil
	}
	if strings.HasPrefix(line, "keyword:") {
		v := strings.TrimSpace(strings.TrimPrefix(line, "keyword:"))
		if v == "" {
			return nil, fmt.Errorf("empty keyword")
		}
		return &ParsedEntry{Kind: EntryDomainKeyword, Value: v, Line: lineNum, Condition: "keyword:" + v}, nil
	}
	if strings.HasPrefix(line, "regexp:") {
		v := strings.TrimSpace(strings.TrimPrefix(line, "regexp:"))
		if _, err := regexp.Compile(v); err != nil {
			return nil, fmt.Errorf("invalid regexp %q: %w", v, err)
		}
		return &ParsedEntry{Kind: EntryDomainRegexp, Value: v, Line: lineNum, Condition: "regexp:" + v}, nil
	}

	// 3. GFWList regex: /pattern/
	if len(line) >= 2 && strings.HasPrefix(line, "/") && strings.HasSuffix(line, "/") {
		pat := line[1 : len(line)-1]
		if _, err := regexp.Compile(pat); err != nil {
			return nil, fmt.Errorf("invalid gfwlist regexp %q: %w", pat, err)
		}
		return &ParsedEntry{Kind: EntryDomainRegexp, Value: pat, Line: lineNum, Condition: "regexp:" + pat}, nil
	}

	// 4. IP / CIDR check (plain IP or prefix)
	if pfx, err := netip.ParsePrefix(line); err == nil {
		pfx = pfx.Masked()
		if pfx.Addr().Is4() {
			return &ParsedEntry{Kind: EntryIPv4CIDR, Value: pfx.String(), Line: lineNum, Condition: pfx.String()}, nil
		}
		return &ParsedEntry{Kind: EntryIPv6CIDR, Value: pfx.String(), Line: lineNum, Condition: pfx.String()}, nil
	}
	if addr, err := netip.ParseAddr(line); err == nil {
		if addr.Is4() {
			v := addr.String() + "/32"
			return &ParsedEntry{Kind: EntryIPv4CIDR, Value: v, Line: lineNum, Condition: v}, nil
		}
		v := addr.String() + "/128"
		return &ParsedEntry{Kind: EntryIPv6CIDR, Value: v, Line: lineNum, Condition: v}, nil
	}

	// 5. GFWList domain patterns: ||example.com, |http://example.com/..., .example.com
	if strings.HasPrefix(line, "||") {
		host := extractGFWHost(strings.TrimPrefix(line, "||"))
		if !isValidDomain(host) {
			return nil, fmt.Errorf("invalid gfwlist domain %q", line)
		}
		return &ParsedEntry{Kind: EntryDomainSuffix, Value: host, Line: lineNum, Condition: "domain:" + host}, nil
	}
	if strings.HasPrefix(line, "|http://") || strings.HasPrefix(line, "|https://") {
		rawURL := strings.TrimPrefix(line, "|")
		host := extractGFWHost(rawURL)
		if !isValidDomain(host) {
			return nil, fmt.Errorf("invalid gfwlist URL host %q", line)
		}
		return &ParsedEntry{Kind: EntryDomainSuffix, Value: host, Line: lineNum, Condition: "domain:" + host}, nil
	}
	if strings.HasPrefix(line, ".") {
		host := extractGFWHost(strings.TrimPrefix(line, "."))
		if !isValidDomain(host) {
			return nil, fmt.Errorf("invalid dot-domain %q", line)
		}
		return &ParsedEntry{Kind: EntryDomainSuffix, Value: host, Line: lineNum, Condition: "domain:" + host}, nil
	}

	// 6. Plain domain (or URL containing domain)
	host := extractGFWHost(line)
	if isValidDomain(host) {
		if listKind == "ipv4_cidr" || listKind == "ipv6_cidr" {
			return nil, fmt.Errorf("expected CIDR in %s list, got domain %q", listKind, line)
		}
		return &ParsedEntry{Kind: EntryDomainSuffix, Value: host, Line: lineNum, Condition: "domain:" + host}, nil
	}
	return nil, fmt.Errorf("unrecognized rule entry %q", line)
}

func extractGFWHost(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	if idx := strings.IndexAny(s, "/?#^"); idx >= 0 {
		s = s[:idx]
	}
	if idx := strings.Index(s, ":"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimPrefix(s, "*.")
	s = strings.TrimPrefix(s, ".")
	return strings.ToLower(strings.TrimSpace(s))
}

func isValidDomain(d string) bool {
	if len(d) == 0 || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	if strings.Contains(d, "..") {
		return false
	}
	return reDomain.MatchString(d)
}

// DiffEntryCounts returns (added, removed) counts between oldRaw and newRaw.
func DiffEntryCounts(oldRaw, newRaw, kind string) (added int, removed int) {
	oldParsed := ParseRuleList(oldRaw, kind)
	newParsed := ParseRuleList(newRaw, kind)
	oldSet := make(map[string]struct{}, len(oldParsed.Entries))
	for _, e := range oldParsed.Entries {
		oldSet[e.Condition] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(newParsed.Entries))
	for _, e := range newParsed.Entries {
		newSet[e.Condition] = struct{}{}
		if _, ok := oldSet[e.Condition]; !ok {
			added++
		}
	}
	for k := range oldSet {
		if _, ok := newSet[k]; !ok {
			removed++
		}
	}
	return added, removed
}
