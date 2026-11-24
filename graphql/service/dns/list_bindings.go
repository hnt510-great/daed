/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package dns

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae-wing/graphql/service/rulelist"
	"gorm.io/gorm"
)

type DomainListBinding struct {
	ListID   uint   `json:"listId"`
	Upstream string `json:"upstream"`
	QType    string `json:"qtype,omitempty"` // optional: a | aaaa | https | svcb | ...
}

func ParseDomainListBindings(raw string) ([]DomainListBinding, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var bindings []DomainListBinding
	if err := json.Unmarshal([]byte(raw), &bindings); err != nil {
		return nil, err
	}
	return bindings, nil
}

func SetDomainListBindings(ctx context.Context, dnsID uint, bindings []DomainListBinding) (*db.Dns, error) {
	b, err := json.Marshal(bindings)
	if err != nil {
		return nil, err
	}
	tx := db.BeginTx(ctx)
	defer tx.Rollback()
	var m db.Dns
	if err := tx.Where("id = ?", dnsID).First(&m).Error; err != nil {
		return nil, err
	}
	m.DomainListRefsJSON = string(b)
	m.Version++
	if err := tx.Save(&m).Error; err != nil {
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// ExpandDnsWithRuleLists injects domain-list request rules into a DNS config
// text without mutating the stored raw DNS config.
func ExpandDnsWithRuleLists(d *gorm.DB, m *db.Dns) (string, []uint, []string, error) {
	if m == nil {
		return "", nil, nil, fmt.Errorf("nil dns model")
	}
	bindings, err := ParseDomainListBindings(m.DomainListRefsJSON)
	if err != nil {
		return "", nil, nil, fmt.Errorf("invalid dns domain list bindings: %w", err)
	}
	if len(bindings) == 0 {
		return m.Dns, nil, nil, nil
	}

	var allLists []db.RuleList
	if d != nil {
		_ = d.Order("id asc").Find(&allLists).Error
	}
	listByID := make(map[uint]db.RuleList, len(allLists))
	for _, l := range allLists {
		listByID[l.ID] = l
	}

	var injected []string
	var warnings []string
	used := make(map[uint]struct{})

	for _, b := range bindings {
		rl, ok := listByID[b.ListID]
		if !ok {
			return "", nil, nil, fmt.Errorf("dns binding references missing rule list ID %d", b.ListID)
		}
		if strings.TrimSpace(b.Upstream) == "" {
			return "", nil, nil, fmt.Errorf("dns binding for rule list %q requires an upstream", rl.Name)
		}
		used[rl.ID] = struct{}{}
		parsed := rulelist.ParseRuleList(rl.LastContent, rl.Kind)
		var qnames []string
		for _, e := range parsed.Entries {
			if e.Kind == rulelist.EntryIPv4CIDR || e.Kind == rulelist.EntryIPv6CIDR {
				continue
			}
			cond := e.Condition
			if idx := strings.Index(cond, ":"); idx > 0 {
				k := cond[:idx]
				if k == "domain" {
					k = "suffix"
				}
				v := strings.ReplaceAll(cond[idx+1:], "'", "\\'")
				qnames = append(qnames, fmt.Sprintf("%s:'%s'", k, v))
			} else {
				qnames = append(qnames, fmt.Sprintf("suffix:'%s'", strings.ReplaceAll(cond, "'", "\\'")))
			}
		}
		if len(qnames) == 0 {
			warnings = append(warnings, fmt.Sprintf("dns binding for rule list %q has no domain entries", rl.Name))
			continue
		}
		cond := fmt.Sprintf("qname(%s)", strings.Join(qnames, ", "))
		if qt := strings.TrimSpace(b.QType); qt != "" {
			cond = fmt.Sprintf("qtype(%s) && %s", qt, cond)
		}
		injected = append(injected, fmt.Sprintf("    %s -> %s", cond, strings.TrimSpace(b.Upstream)))
	}

	var usedIDs []uint
	for id := range used {
		usedIDs = append(usedIDs, id)
	}
	sort.Slice(usedIDs, func(i, j int) bool { return usedIDs[i] < usedIDs[j] })

	if len(injected) == 0 {
		return m.Dns, usedIDs, warnings, nil
	}

	// Inject right after "request {" inside routing { ... } of dns config.
	text := m.Dns
	block := "\n" + strings.Join(injected, "\n") + "\n"
	idx := strings.Index(text, "request {")
	if idx >= 0 {
		pos := idx + len("request {")
		return text[:pos] + block + text[pos:], usedIDs, warnings, nil
	}
	idx = strings.Index(text, "request{")
	if idx >= 0 {
		pos := idx + len("request{")
		return text[:pos] + block + text[pos:], usedIDs, warnings, nil
	}
	return "", usedIDs, warnings, fmt.Errorf("dns config %q has no request { } block to bind rule lists", m.Name)
}
