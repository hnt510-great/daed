/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/daeuniverse/dae-wing/dae"
	"github.com/daeuniverse/dae-wing/db"
	dnsSvc "github.com/daeuniverse/dae-wing/graphql/service/dns"
	routingSvc "github.com/daeuniverse/dae-wing/graphql/service/routing"
	"github.com/daeuniverse/dae/common/assets"
	"github.com/daeuniverse/dae/common/consts"
	daeDns "github.com/daeuniverse/dae/component/dns"
	"github.com/daeuniverse/dae/component/outbound/dialer"
	daeRouting "github.com/daeuniverse/dae/component/routing"
	daeConfig "github.com/daeuniverse/dae/config"
	"github.com/daeuniverse/dae/control"
	"github.com/daeuniverse/dae/pkg/config_parser"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type SnapshotNode struct {
	MemberKey  string `json:"memberKey"`  // "node:<id>" or "chain:<id>"
	UniqueName string `json:"uniqueName"` // dae node key
	Link       string `json:"link"`       // actual link or chain ("a -> b")
	Display    string `json:"display"`
	Protocol   string `json:"protocol"`
}

type SnapshotGroup struct {
	GroupID      uint              `json:"groupId"`
	Name         string            `json:"name"`
	Version      uint              `json:"version"`
	Policy       string            `json:"policy"`
	PolicyParams []PolicyParamSnap `json:"policyParams,omitempty"`
	MemberKeys   []string          `json:"memberKeys"`
	MemberNames  []string          `json:"memberNames"`
	Failover     bool              `json:"failover"`
}

type PolicyParamSnap struct {
	Key string `json:"key"`
	Val string `json:"val"`
}

type CandidateDiagnostic struct {
	Scope   string `json:"scope"` // global | dns | routing | group | node | chain | rulelist | capacity
	Field   string `json:"field"`
	Level   string `json:"level"` // error | warning
	Message string `json:"message"`
}

type CapacityEstimate struct {
	EstimatedMatchSets int  `json:"estimatedMatchSets"`
	MaxMatchSets       int  `json:"maxMatchSets"`
	RuleCount          int  `json:"ruleCount"`
	Exceeded           bool `json:"exceeded"`
}

type Candidate struct {
	ConfigModel         db.Config
	DnsModel            db.Dns
	RoutingModel        db.Routing
	DaeConfig           *daeConfig.Config
	GlobalText          string
	DnsText             string
	RoutingText         string
	ExpandedRules       []string
	Nodes               []SnapshotNode
	Groups              []SnapshotGroup
	GroupModels         []db.Group
	DependencyVersions  map[string]string
	Diagnostics         []CandidateDiagnostic
	Capacity            CapacityEstimate
	Digest              string
	RedactedDiff        string
	HasEffectiveChanges bool
	RoutingTraceEnabled bool
}

type AssembleOptions struct {
	ConfigID          *uint
	DnsID             *uint
	RoutingID         *uint
	ExternGeoDataDirs []string
	ValidateDialers   bool
}

func maxMatchSetLimit() int {
	if n, err := strconv.Atoi(consts.MaxMatchSetLen_); err == nil && n > 0 {
		return n
	}
	return 1024
}

// AssembleCandidate builds a deterministic, side-effect-free candidate config
// from a consistent DB handle (call inside a read-only transaction for preview).
func AssembleCandidate(d *gorm.DB, opts AssembleOptions) (*Candidate, error) {
	cand := &Candidate{
		DependencyVersions: make(map[string]string),
	}

	// 1. Load Global, DNS, Routing
	var mConfig db.Config
	var mDns db.Dns
	var mRouting db.Routing

	qCfg := d.Model(&db.Config{})
	if opts.ConfigID != nil && *opts.ConfigID > 0 {
		qCfg = qCfg.Where("id = ?", *opts.ConfigID)
	} else {
		qCfg = qCfg.Where("selected = ?", true)
	}
	if err := qCfg.First(&mConfig).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("please select a config")
		}
		return nil, err
	}

	qDns := d.Model(&db.Dns{})
	if opts.DnsID != nil && *opts.DnsID > 0 {
		qDns = qDns.Where("id = ?", *opts.DnsID)
	} else {
		qDns = qDns.Where("selected = ?", true)
	}
	if err := qDns.First(&mDns).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("please select a dns")
		}
		return nil, err
	}

	qRouting := d.Model(&db.Routing{})
	if opts.RoutingID != nil && *opts.RoutingID > 0 {
		qRouting = qRouting.Where("id = ?", *opts.RoutingID)
	} else {
		qRouting = qRouting.Where("selected = ?", true)
	}
	if err := qRouting.First(&mRouting).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("please select a routing")
		}
		return nil, err
	}

	cand.ConfigModel = mConfig
	cand.DnsModel = mDns
	cand.RoutingModel = mRouting
	cand.GlobalText = mConfig.Global
	cand.RoutingTraceEnabled = mConfig.RoutingTraceEnabled
	cand.DependencyVersions[fmt.Sprintf("config:%d", mConfig.ID)] = fmt.Sprintf("v%d", mConfig.Version)
	cand.DependencyVersions[fmt.Sprintf("dns:%d", mDns.ID)] = fmt.Sprintf("v%d", mDns.Version)
	cand.DependencyVersions[fmt.Sprintf("routing:%d", mRouting.ID)] = fmt.Sprintf("v%d-draft%d", mRouting.Version, mRouting.DraftRevision)

	// 2. Compile Routing (visual or raw)
	routingText := mRouting.Routing
	if mRouting.EditMode == db.RoutingEditModeVisual {
		var vp routingSvc.VisualPolicy
		if strings.TrimSpace(mRouting.VisualPolicyJSON) != "" {
			if err := json.Unmarshal([]byte(mRouting.VisualPolicyJSON), &vp); err != nil {
				cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
					Scope:   "routing",
					Field:   "visualPolicy",
					Level:   "error",
					Message: fmt.Sprintf("invalid visual policy JSON: %v", err),
				})
			}
		}
		compiled, err := routingSvc.CompileVisualPolicy(d, &vp)
		if err != nil {
			return nil, err
		}
		routingText = compiled.RoutingText
		cand.ExpandedRules = compiled.Lines
		for _, diag := range compiled.Diagnostics {
			cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
				Scope:   "routing",
				Field:   diag.Field,
				Level:   diag.Level,
				Message: diag.Message,
			})
		}
		for _, lid := range compiled.ListIDs {
			var rl db.RuleList
			if err := d.Where("id = ?", lid).First(&rl).Error; err == nil {
				cand.DependencyVersions[fmt.Sprintf("rulelist:%d", rl.ID)] = fmt.Sprintf("v%d:%s", rl.Version, shortDigest(rl.LastDigest))
			}
		}
	} else {
		for _, l := range strings.Split(strings.ReplaceAll(routingText, "\r\n", "\n"), "\n") {
			l = strings.TrimSpace(l)
			if l != "" && l != "routing {" && l != "}" {
				cand.ExpandedRules = append(cand.ExpandedRules, l)
			}
		}
	}
	cand.RoutingText = routingText

	// 3. Expand DNS with rule-list bindings
	dnsText, dnsListIDs, dnsWarnings, err := dnsSvc.ExpandDnsWithRuleLists(d, &mDns)
	if err != nil {
		cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
			Scope:   "dns",
			Field:   "domainListRefs",
			Level:   "error",
			Message: err.Error(),
		})
		dnsText = mDns.Dns
	}
	for _, w := range dnsWarnings {
		cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
			Scope:   "dns",
			Field:   "domainListRefs",
			Level:   "warning",
			Message: w,
		})
	}
	for _, lid := range dnsListIDs {
		var rl db.RuleList
		if err := d.Where("id = ?", lid).First(&rl).Error; err == nil {
			cand.DependencyVersions[fmt.Sprintf("rulelist:%d", rl.ID)] = fmt.Sprintf("v%d:%s", rl.Version, shortDigest(rl.LastDigest))
		}
	}
	cand.DnsText = dnsText

	// 4. Parse Global + DNS + Routing
	daeConf, err := dae.ParseConfig(&cand.GlobalText, &cand.DnsText, &cand.RoutingText)
	if err != nil {
		cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
			Scope:   "routing",
			Field:   "syntax",
			Level:   "error",
			Message: err.Error(),
		})
		return cand, nil
	}
	cand.DaeConfig = daeConf

	// 5. Determine necessary groups (routing outbounds + forced download groups)
	necessarySet := make(map[string]struct{})
	for _, name := range dae.NecessaryOutbounds(&daeConf.Routing) {
		switch name {
		case "direct", "block", "must_rules":
		default:
			necessarySet[name] = struct{}{}
		}
	}
	// Include forced download groups from subscriptions and rule lists.
	var subs []db.Subscription
	if err := d.Where("download_mode = ? AND download_group_name <> ''", "group").Order("id asc").Find(&subs).Error; err == nil {
		for _, s := range subs {
			necessarySet[s.DownloadGroupName] = struct{}{}
		}
	}
	var rls []db.RuleList
	if err := d.Where("download_mode = ? AND download_group_name <> ''", "group").Order("id asc").Find(&rls).Error; err == nil {
		for _, rl := range rls {
			necessarySet[rl.DownloadGroupName] = struct{}{}
		}
	}

	necessaryNames := make([]string, 0, len(necessarySet))
	for name := range necessarySet {
		necessaryNames = append(necessaryNames, name)
	}
	sort.Strings(necessaryNames)

	var groups []db.Group
	if len(necessaryNames) > 0 {
		if err := d.Model(&db.Group{}).
			Where("name in ?", necessaryNames).
			Order("name asc, id asc").
			Preload("PolicyParams").
			Preload("Chains").
			Preload("SubscriptionBindings").
			Preload("SubscriptionBindings.Subscription").
			Preload("SubscriptionBindings.Subscription.Node").
			Find(&groups).Error; err != nil {
			return nil, err
		}
	}
	groupByName := make(map[string]db.Group, len(groups))
	for _, g := range groups {
		groupByName[g.Name] = g
	}
	for _, name := range necessaryNames {
		if _, ok := groupByName[name]; !ok {
			cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
				Scope:   "group",
				Field:   name,
				Level:   "error",
				Message: fmt.Sprintf("group %q is referenced but not defined", name),
			})
		}
	}
	cand.GroupModels = groups

	// Preload failover configs for all necessary groups.
	failoverByGroupID := make(map[uint]db.GroupFailover)
	if len(groups) > 0 {
		var gids []uint
		for _, g := range groups {
			gids = append(gids, g.ID)
		}
		var fcs []db.GroupFailover
		if err := d.Where("group_id in ?", gids).Find(&fcs).Error; err == nil {
			for _, fc := range fcs {
				failoverByGroupID[fc.GroupID] = fc
			}
		}
	}

	// Load all nodes by ID for resolving proxy chains cleanly.
	var allNodes []db.Node
	if err := d.Order("id asc").Find(&allNodes).Error; err != nil {
		return nil, err
	}
	nodeByID := make(map[uint]db.Node, len(allNodes))
	for _, n := range allNodes {
		nodeByID[n.ID] = n
	}

	// 6. Deterministic node & chain resolution across groups
	type resolvedMember struct {
		memberKey      string
		link           string
		display        string
		protocol       string
		tag            *string
		subscriptionID *uint
		sortID         uint
		uniqueName     string
	}
	membersByKey := make(map[string]*resolvedMember)
	groupMemberKeys := make(map[uint][]string)

	for i := range groups {
		g := &groups[i]
		cand.DependencyVersions[fmt.Sprintf("group:%d", g.ID)] = fmt.Sprintf("v%d", g.Version)
		seenInGroup := make(map[string]struct{})
		var orderedKeys []string
		addMember := func(rm *resolvedMember) {
			if _, ok := membersByKey[rm.memberKey]; !ok {
				membersByKey[rm.memberKey] = rm
			}
			if _, ok := seenInGroup[rm.memberKey]; !ok {
				seenInGroup[rm.memberKey] = struct{}{}
				orderedKeys = append(orderedKeys, rm.memberKey)
			}
		}

		// Subscription bindings (sorted by SubscriptionID)
		bindings := append([]db.GroupSubscription{}, g.SubscriptionBindings...)
		sort.Slice(bindings, func(a, b int) bool { return bindings[a].SubscriptionID < bindings[b].SubscriptionID })
		for bi := range bindings {
			b := &bindings[bi]
			cand.DependencyVersions[fmt.Sprintf("subscription:%d", b.SubscriptionID)] = fmt.Sprintf("rev%d", b.Subscription.Revision)
			matched, err := matchedNodesForGroupSubscription(b)
			if err != nil {
				cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
					Scope:   "group",
					Field:   g.Name,
					Level:   "error",
					Message: fmt.Sprintf("group %q has invalid subscription regex: %v", g.Name, err),
				})
				continue
			}
			sort.Slice(matched, func(a, c int) bool { return matched[a].ID < matched[c].ID })
			for _, n := range matched {
				if n.NeedsRebind {
					cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
						Scope:   "node",
						Field:   fmt.Sprintf("node:%d", n.ID),
						Level:   "error",
						Message: fmt.Sprintf("node %q in group %q needs rebind: %s", n.Name, g.Name, n.RebindReason),
					})
					continue
				}
				nCopy := n
				addMember(&resolvedMember{
					memberKey:      fmt.Sprintf("node:%d", n.ID),
					link:           n.Link,
					display:        n.Name,
					protocol:       n.Protocol,
					tag:            nCopy.Tag,
					subscriptionID: nCopy.SubscriptionID,
					sortID:         n.ID,
				})
			}
		}

		// Solitary/explicit nodes (sorted by ID)
		var solitaryNodes []db.Node
		if err := d.Model(g).Association("Node").Find(&solitaryNodes); err != nil {
			return nil, err
		}
		sort.Slice(solitaryNodes, func(a, b int) bool { return solitaryNodes[a].ID < solitaryNodes[b].ID })
		for _, n := range solitaryNodes {
			if n.NeedsRebind {
				cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
					Scope:   "node",
					Field:   fmt.Sprintf("node:%d", n.ID),
					Level:   "error",
					Message: fmt.Sprintf("node %q in group %q needs rebind: %s", n.Name, g.Name, n.RebindReason),
				})
				continue
			}
			nCopy := n
			addMember(&resolvedMember{
				memberKey:      fmt.Sprintf("node:%d", n.ID),
				link:           n.Link,
				display:        n.Name,
				protocol:       n.Protocol,
				tag:            nCopy.Tag,
				subscriptionID: nCopy.SubscriptionID,
				sortID:         n.ID,
			})
		}

		// Proxy chains attached to group (sorted by ID)
		chains := append([]db.ProxyChain{}, g.Chains...)
		sort.Slice(chains, func(a, b int) bool { return chains[a].ID < chains[b].ID })
		for _, ch := range chains {
			cand.DependencyVersions[fmt.Sprintf("chain:%d", ch.ID)] = fmt.Sprintf("v%d", ch.Version)
			if ch.NeedsRebind {
				cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
					Scope:   "chain",
					Field:   fmt.Sprintf("chain:%d", ch.ID),
					Level:   "error",
					Message: fmt.Sprintf("proxy chain %q in group %q needs rebind: %s", ch.Name, g.Name, ch.RebindReason),
				})
				continue
			}
			var hopIDs []uint
			if err := json.Unmarshal([]byte(ch.MemberNodeIDsJSON), &hopIDs); err != nil || len(hopIDs) < 2 {
				cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
					Scope:   "chain",
					Field:   fmt.Sprintf("chain:%d", ch.ID),
					Level:   "error",
					Message: fmt.Sprintf("proxy chain %q must have at least 2 leaf nodes", ch.Name),
				})
				continue
			}
			var hopLinks []string
			broken := false
			for _, hid := range hopIDs {
				leaf, ok := nodeByID[hid]
				if !ok || leaf.NeedsRebind {
					cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
						Scope:   "chain",
						Field:   fmt.Sprintf("chain:%d", ch.ID),
						Level:   "error",
						Message: fmt.Sprintf("proxy chain %q references missing or invalid leaf node %d", ch.Name, hid),
					})
					broken = true
					break
				}
				hopLinks = append(hopLinks, leaf.Link)
			}
			if broken {
				continue
			}
			chainTag := fmt.Sprintf("chain_%d_%s", ch.ID, normNodeName(ch.Name))
			if ch.Tag != nil && *ch.Tag != "" {
				chainTag = *ch.Tag
			}
			addMember(&resolvedMember{
				memberKey: fmt.Sprintf("chain:%d", ch.ID),
				link:      strings.Join(hopLinks, " -> "),
				display:   ch.Name,
				protocol:  "chain",
				tag:       &chainTag,
				sortID:    1_000_000 + ch.ID,
			})
		}

		// If failover is enabled on this group, reorder members to Primary + Backups + remaining!
		if fc, ok := failoverByGroupID[g.ID]; ok && fc.Enabled {
			cand.DependencyVersions[fmt.Sprintf("failover:%d", g.ID)] = fmt.Sprintf("rev%d", fc.Revision)
			var backupKeys []string
			_ = json.Unmarshal([]byte(fc.BackupMemberKeysJSON), &backupKeys)
			desired := make([]string, 0, 1+len(backupKeys))
			if fc.PrimaryMemberKey != "" {
				desired = append(desired, fc.PrimaryMemberKey)
			}
			desired = append(desired, backupKeys...)

			reordered := make([]string, 0, len(orderedKeys))
			placed := make(map[string]struct{})
			for _, k := range desired {
				if _, exists := seenInGroup[k]; !exists {
					cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
						Scope:   "group",
						Field:   g.Name,
						Level:   "error",
						Message: fmt.Sprintf("failover config for group %q references member %q which is not in the group or needs rebind", g.Name, k),
					})
					continue
				}
				if _, dup := placed[k]; !dup {
					placed[k] = struct{}{}
					reordered = append(reordered, k)
				}
			}
			for _, k := range orderedKeys {
				if _, dup := placed[k]; !dup {
					reordered = append(reordered, k)
				}
			}
			orderedKeys = reordered
		}

		groupMemberKeys[g.ID] = orderedKeys
	}

	// Deterministic unique node naming (NO map iteration randomness!)
	allMembers := make([]*resolvedMember, 0, len(membersByKey))
	for _, rm := range membersByKey {
		allMembers = append(allMembers, rm)
	}
	sort.Slice(allMembers, func(i, j int) bool {
		hi := allMembers[i].tag != nil && *allMembers[i].tag != ""
		hj := allMembers[j].tag != nil && *allMembers[j].tag != ""
		if hi != hj {
			return hi
		}
		if allMembers[i].sortID != allMembers[j].sortID {
			return allMembers[i].sortID < allMembers[j].sortID
		}
		return allMembers[i].memberKey < allMembers[j].memberKey
	})

	usedNames := make(map[string]struct{}, len(allMembers))
	for _, rm := range allMembers {
		var base string
		if rm.tag != nil && *rm.tag != "" {
			base = normNodeName(*rm.tag)
		} else {
			base = normNodeName(rm.display)
			if base == "" {
				base = rm.memberKey
			}
			if rm.subscriptionID != nil {
				base = fmt.Sprintf("%d.%s", *rm.subscriptionID, base)
			}
		}
		wanted := base
		for suffix := 1; ; suffix++ {
			if _, exists := usedNames[wanted]; !exists {
				usedNames[wanted] = struct{}{}
				rm.uniqueName = wanted
				break
			}
			wanted = fmt.Sprintf("%s.%d", base, suffix)
		}
	}

	// Build daeConfig.Group & SnapshotGroup in deterministic order.
	for i := range groups {
		g := &groups[i]
		mKeys := groupMemberKeys[g.ID]
		if len(mKeys) == 0 {
			cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
				Scope:   "group",
				Field:   g.Name,
				Level:   "error",
				Message: fmt.Sprintf("please add at least one node into group '%v' (referenced by current routing '%v')", g.Name, mRouting.Name),
			})
			continue
		}

		fc, hasFailover := failoverByGroupID[g.ID]
		failoverActive := hasFailover && fc.Enabled

		var policy daeConfig.FunctionListOrString
		var policyName string
		var snapParams []PolicyParamSnap

		if failoverActive && len(mKeys) > 1 {
			// Assemble multi-member failover group as dae native `select` policy
			// starting at index 0 (primary member).
			policyName = "select"
			policy = &config_parser.Function{
				Name: "select",
				Params: []*config_parser.Param{
					{Val: "0"},
				},
			}
			snapParams = []PolicyParamSnap{{Val: "0"}}
		} else {
			policyName = g.Policy
			if g.Policy == "fixed" && len(mKeys) > 1 {
				cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
					Scope:   "group",
					Field:   g.Name,
					Level:   "error",
					Message: fmt.Sprintf("group '%v' with policy 'fixed' cannot have more than one node", g.Name),
				})
				continue
			}
			if len(g.PolicyParams) == 0 {
				policy = g.Policy
			} else {
				params := make([]*config_parser.Param, 0, len(g.PolicyParams))
				for _, p := range g.PolicyParams {
					params = append(params, p.Marshal())
					snapParams = append(snapParams, PolicyParamSnap{Key: p.Key, Val: p.Value})
				}
				policy = &config_parser.Function{
					Name:   g.Policy,
					Params: params,
				}
			}
		}

		var nameParams []*config_parser.Param
		var memberNames []string
		for _, mk := range mKeys {
			rm := membersByKey[mk]
			nameParams = append(nameParams, &config_parser.Param{Val: rm.uniqueName})
			memberNames = append(memberNames, rm.uniqueName)
		}
		grp := daeConfig.Group{
			Name: g.Name,
			Filter: [][]*config_parser.Function{{{
				Name:   "name",
				Params: nameParams,
			}}},
			FilterAnnotation: [][]*config_parser.Param{{}},
			Policy:           policy,
		}
		daeConf.Group = append(daeConf.Group, grp)
		cand.Groups = append(cand.Groups, SnapshotGroup{
			GroupID:      g.ID,
			Name:         g.Name,
			Version:      g.Version,
			Policy:       policyName,
			PolicyParams: snapParams,
			MemberKeys:   mKeys,
			MemberNames:  memberNames,
			Failover:     failoverActive,
		})
	}

	// Fill daeConfig.Node & SnapshotNode in deterministic order.
	for _, rm := range allMembers {
		daeConf.Node = append(daeConf.Node, daeConfig.KeyableString(fmt.Sprintf("%s:%s", rm.uniqueName, rm.link)))
		cand.Nodes = append(cand.Nodes, SnapshotNode{
			MemberKey:  rm.memberKey,
			UniqueName: rm.uniqueName,
			Link:       rm.link,
			Display:    rm.display,
			Protocol:   rm.protocol,
		})
	}

	// 7. Side-effect-free validation: routing optimizer + DNS + dialers + capacity
	geoDirs := opts.ExternGeoDataDirs
	if len(geoDirs) == 0 {
		geoDirs = []string{"/etc/daed", "/usr/share/dae"}
	}
	validateCandidateRulesAndCapacity(cand, geoDirs)

	if opts.ValidateDialers {
		noLog := logrus.New()
		noLog.SetOutput(io.Discard)
		for _, sn := range cand.Nodes {
			dl, err := dialer.NewFromLinkContext(context.Background(), &dialer.GlobalOption{Log: noLog}, dialer.InstanceOption{DisableCheck: true}, sn.Link, sn.UniqueName)
			if err != nil {
				cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
					Scope:   "node",
					Field:   sn.MemberKey,
					Level:   "error",
					Message: fmt.Sprintf("node %q dialer check failed: %v", sn.Display, err),
				})
				continue
			}
			_ = dl.Close()
		}
	}

	// 8. Compute canonical deterministic SHA-256 digest
	cand.Digest = computeCandidateDigest(cand)

	// 9. Compute redacted diff against currently active snapshot
	var activeSnap db.ConfigSnapshot
	if err := d.Where("active = ?", true).Order("id desc").First(&activeSnap).Error; err == nil {
		cand.HasEffectiveChanges = activeSnap.Digest != cand.Digest
		cand.RedactedDiff = buildRedactedDiff(&activeSnap, cand)
	} else {
		cand.HasEffectiveChanges = true
		cand.RedactedDiff = "+ (initial configuration snapshot)"
	}

	return cand, nil
}

func validateCandidateRulesAndCapacity(cand *Candidate, geoDirs []string) {
	if cand.DaeConfig == nil {
		return
	}
	noLog := logrus.New()
	noLog.SetOutput(io.Discard)

	// Routing optimizer & parser validation (mirrors dae/cmd/validate.go)
	program, err := daeRouting.NewNormalizedProgram(
		cand.DaeConfig.Routing.Rules,
		cand.DaeConfig.Routing.Fallback,
		&daeRouting.AliasOptimizer{},
		&daeRouting.DatReaderOptimizer{Logger: noLog, LocationFinder: assets.NewLocationFinder(geoDirs)},
		&daeRouting.MergeAndSortRulesOptimizer{},
		&daeRouting.DeduplicateParamsOptimizer{},
	)
	if err != nil {
		cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
			Scope:   "routing",
			Field:   "rules",
			Level:   "error",
			Message: fmt.Sprintf("routing validation failed: %v", err),
		})
	} else {
		outboundNames := map[string]struct{}{
			consts.OutboundDirect.String(): {},
			consts.OutboundBlock.String():  {},
		}
		for _, g := range cand.DaeConfig.Group {
			outboundNames[g.Name] = struct{}{}
		}
		resolveOutbound := func(name string) error {
			switch name {
			case consts.OutboundDirect.String(), consts.OutboundBlock.String(),
				consts.OutboundLogicalOr.String(), consts.OutboundLogicalAnd.String(),
				consts.OutboundMustRules.String(), consts.OutboundControlPlaneRouting.String():
				return nil
			}
			if _, ok := outboundNames[name]; !ok {
				return fmt.Errorf("outbound (group) %q not found", name)
			}
			return nil
		}
		if err := program.Lower(noLog, func(b *daeRouting.RulesBuilder) {
			control.RegisterRoutingProgramParsers(b, resolveOutbound)
		}, func(fallback daeConfig.FunctionOrString) error {
			fn, err := daeConfig.ParseFunctionOrString(fallback)
			if err != nil {
				return err
			}
			out, err := daeRouting.ParseOutbound(fn)
			if err != nil {
				return err
			}
			return resolveOutbound(out.Name)
		}); err != nil {
			cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
				Scope:   "routing",
				Field:   "rules",
				Level:   "error",
				Message: fmt.Sprintf("routing rule lowering failed: %v", err),
			})
		}
		// Estimate match set usage including port ranges and sniff-punt overhead.
		estSets := estimateMatchSets(program, cand.DaeConfig)
		limit := maxMatchSetLimit()
		cand.Capacity = CapacityEstimate{
			EstimatedMatchSets: estSets,
			MaxMatchSets:       limit,
			RuleCount:          len(program.Rules),
			Exceeded:           estSets > limit,
		}
		if estSets > limit {
			cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
				Scope:   "capacity",
				Field:   "routing",
				Level:   "error",
				Message: fmt.Sprintf("estimated routing match sets (%d) exceeds BPF limit (%d)", estSets, limit),
			})
		} else if estSets > (limit*85)/100 {
			cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
				Scope:   "capacity",
				Field:   "routing",
				Level:   "warning",
				Message: fmt.Sprintf("estimated routing match sets (%d) is close to BPF limit (%d)", estSets, limit),
			})
		}
	}

	// DNS routing validation
	if err := daeDns.ValidateRouting(noLog, &cand.DaeConfig.Dns, geoDirs); err != nil {
		cand.Diagnostics = append(cand.Diagnostics, CandidateDiagnostic{
			Scope:   "dns",
			Field:   "routing",
			Level:   "error",
			Message: fmt.Sprintf("DNS validation failed: %v", err),
		})
	}
}

// estimateMatchSets approximates kernel match-set consumption after normalization,
// accounting for port-range expansion (each port range emits a match set) and
// control-plane auto-inserted sniff-punt conditions.
func estimateMatchSets(program *daeRouting.NormalizedProgram, conf *daeConfig.Config) int {
	if program == nil {
		return 0
	}
	total := 0
	for _, r := range program.Rules {
		for _, fn := range r.AndFunctions {
			switch fn.Name {
			case "dport", "sport":
				// Each port or port range item produces a match set in routing_matcher_builder.go.
				if len(fn.Params) == 0 {
					total++
				} else {
					total += len(fn.Params)
				}
			case "domain":
				// Same-class domains share one match set per rule condition.
				total++
			default:
				total++
			}
		}
	}
	// Control plane may insert sniff-punt conditions when dial_mode != ip.
	if conf != nil && conf.Global.DialMode != "ip" && len(program.Rules) > 0 {
		total += 2
	}
	return total
}

func (c *Candidate) HasErrors() bool {
	for _, d := range c.Diagnostics {
		if d.Level == "error" {
			return true
		}
	}
	return false
}

func computeCandidateDigest(c *Candidate) string {
	h := sha256.New()
	_, _ = h.Write([]byte("GLOBAL\n" + c.GlobalText + "\n"))
	_, _ = h.Write([]byte("DNS\n" + c.DnsText + "\n"))
	_, _ = h.Write([]byte("ROUTING\n" + c.RoutingText + "\n"))
	gb, _ := json.Marshal(c.Groups)
	_, _ = h.Write([]byte("GROUPS\n"))
	_, _ = h.Write(gb)
	nb, _ := json.Marshal(c.Nodes)
	_, _ = h.Write([]byte("\nNODES\n"))
	_, _ = h.Write(nb)
	return hex.EncodeToString(h.Sum(nil))
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

func buildRedactedDiff(prev *db.ConfigSnapshot, next *Candidate) string {
	var lines []string
	if prev.GlobalText != next.GlobalText {
		lines = append(lines, "~ global config changed")
	}
	if prev.DnsText != next.DnsText {
		lines = append(lines, "~ dns config changed")
	}
	if prev.RoutingText != next.RoutingText {
		oldLines := strings.Split(strings.TrimSpace(prev.RoutingText), "\n")
		newLines := strings.Split(strings.TrimSpace(next.RoutingText), "\n")
		oldSet := make(map[string]struct{}, len(oldLines))
		for _, l := range oldLines {
			oldSet[l] = struct{}{}
		}
		newSet := make(map[string]struct{}, len(newLines))
		for _, l := range newLines {
			newSet[l] = struct{}{}
		}
		for _, l := range oldLines {
			if _, ok := newSet[l]; !ok {
				lines = append(lines, "- "+l)
			}
		}
		for _, l := range newLines {
			if _, ok := oldSet[l]; !ok {
				lines = append(lines, "+ "+l)
			}
		}
	}
	var prevGroups []SnapshotGroup
	_ = json.Unmarshal([]byte(prev.GroupsJSON), &prevGroups)
	pgJSON, _ := json.Marshal(prevGroups)
	ngJSON, _ := json.Marshal(next.Groups)
	if string(pgJSON) != string(ngJSON) {
		lines = append(lines, fmt.Sprintf("~ groups changed (%d -> %d)", len(prevGroups), len(next.Groups)))
	}
	var prevNodes []SnapshotNode
	_ = json.Unmarshal([]byte(prev.NodesJSON), &prevNodes)
	if len(prevNodes) != len(next.Nodes) {
		lines = append(lines, fmt.Sprintf("~ resolved nodes count changed (%d -> %d; credentials redacted)", len(prevNodes), len(next.Nodes)))
	} else {
		for i := range prevNodes {
			if prevNodes[i].UniqueName != next.Nodes[i].UniqueName || prevNodes[i].Link != next.Nodes[i].Link {
				lines = append(lines, "~ resolved node set updated (credentials redacted)")
				break
			}
		}
	}
	if len(lines) == 0 {
		return "(no effective changes)"
	}
	return strings.Join(lines, "\n")
}

// PreviewPolicy runs a side-effect-free candidate assembly inside a read-only transaction.
func PreviewPolicy(ctx context.Context, opts AssembleOptions) (*Candidate, error) {
	tx := db.BeginReadOnlyTx(ctx)
	defer tx.Rollback()
	opts.ValidateDialers = true
	return AssembleCandidate(tx, opts)
}

type ApplyResult struct {
	Success        bool
	Running        bool
	RolledBack     bool
	SnapshotID     uint
	Digest         string
	Generation     uint64
	Status         string
	Message        string
	Diagnostics    []CandidateDiagnostic
}

// ApplyPolicyRevision verifies optional draft revision & candidate digest,
// reloads the control plane, and commits an immutable ConfigSnapshot.
func ApplyPolicyRevision(ctx context.Context, expectedDraftRev *uint, expectedDigest *string, opts AssembleOptions) (*ApplyResult, error) {
	if ok := runLock.TryLock(); !ok {
		return nil, fmt.Errorf("another apply or reload operation is already in progress")
	}
	defer runLock.Unlock()

	return applyLocked(db.DB(ctx), expectedDraftRev, expectedDigest, opts)
}

func applyLocked(d *gorm.DB, expectedDraftRev *uint, expectedDigest *string, opts AssembleOptions) (*ApplyResult, error) {
	opts.ValidateDialers = true
	cand, err := AssembleCandidate(d, opts)
	if err != nil {
		return nil, err
	}
	if expectedDraftRev != nil && cand.RoutingModel.DraftRevision != *expectedDraftRev {
		return nil, fmt.Errorf("draft revision mismatch: current is %d, expected %d", cand.RoutingModel.DraftRevision, *expectedDraftRev)
	}
	if expectedDigest != nil && *expectedDigest != "" && cand.Digest != *expectedDigest {
		return nil, fmt.Errorf("candidate digest mismatch: current is %s, expected %s; please re-preview", shortDigest(cand.Digest), shortDigest(*expectedDigest))
	}
	if cand.HasErrors() {
		return &ApplyResult{
			Success:     false,
			Status:      "apply_failed",
			Message:     cand.Diagnostics[0].Message,
			Diagnostics: cand.Diagnostics,
			Digest:      cand.Digest,
		}, fmt.Errorf("candidate validation failed: %s", cand.Diagnostics[0].Message)
	}

	// Capture previous active snapshot for rollback if DB commit fails after reload.
	var prevSnap db.ConfigSnapshot
	hasPrevSnap := d.Where("active = ?", true).Order("id desc").First(&prevSnap).Error == nil

	// Reload control plane.
	chReload := make(chan error, 1)
	dae.ChReloadConfigs <- &dae.ReloadMessage{
		Config:              cand.DaeConfig,
		RoutingTraceEnabled: cand.RoutingTraceEnabled,
		Callback:            chReload,
	}
	if reloadErr := <-chReload; reloadErr != nil {
		// Control plane automatically rolled back to previous config in dae.Run.
		_ = updateSystemApplyFailure(d, reloadErr.Error(), hasPrevSnap)
		return &ApplyResult{
			Success:    false,
			Running:    hasPrevSnap,
			RolledBack: hasPrevSnap,
			Status:     "apply_failed",
			Message:    fmt.Sprintf("failed to load new config: %v", reloadErr),
			Digest:     cand.Digest,
			Generation: dae.ActiveGeneration(),
		}, fmt.Errorf("failed to load new config: %w", reloadErr)
	}

	// Commit snapshot and System state.
	snapID, commitErr := commitSnapshotAndSystem(d, cand)
	if commitErr != nil {
		// Candidate is running in memory, but DB commit failed! Restore previous snapshot.
		rolledBack := false
		if hasPrevSnap {
			if prevConf, err := buildDaeConfigFromSnapshot(&prevSnap); err == nil {
				chRb := make(chan error, 1)
				dae.ChReloadConfigs <- &dae.ReloadMessage{
					Config:              prevConf,
					RoutingTraceEnabled: true,
					Callback:            chRb,
				}
				if rbErr := <-chRb; rbErr == nil {
					rolledBack = true
				}
			}
		}
		if !rolledBack {
			// Stop datapath so UI never lies about running state.
			chStop := make(chan error, 1)
			dae.ChReloadConfigs <- &dae.ReloadMessage{Config: dae.EmptyConfig, Callback: chStop}
			<-chStop
			_ = d.Model(&db.System{}).Where("id > 0").Updates(map[string]interface{}{
				"running":           false,
				"last_apply_status": "apply_failed",
				"last_apply_error":  fmt.Sprintf("DB commit failed and rollback failed: %v", commitErr),
			}).Error
		}
		return &ApplyResult{
			Success:    false,
			Running:    rolledBack,
			RolledBack: rolledBack,
			Status:     "apply_failed",
			Message:    fmt.Sprintf("DB commit failed: %v", commitErr),
			Generation: dae.ActiveGeneration(),
		}, commitErr
	}

	return &ApplyResult{
		Success:    true,
		Running:    true,
		SnapshotID: snapID,
		Digest:     cand.Digest,
		Generation: dae.ActiveGeneration(),
		Status:     "running",
		Message:    "applied successfully",
	}, nil
}

func updateSystemApplyFailure(d *gorm.DB, errMsg string, stillRunning bool) error {
	var sys db.System
	if err := d.Model(&db.System{}).FirstOrCreate(&sys).Error; err != nil {
		return err
	}
	return d.Model(&sys).Updates(map[string]interface{}{
		"running":           stillRunning && sys.Running,
		"last_apply_status": "apply_failed",
		"last_apply_error":  errMsg,
	}).Error
}

func commitSnapshotAndSystem(d *gorm.DB, cand *Candidate) (uint, error) {
	var snapID uint
	writeFn := func(tx *gorm.DB) error {
		nodesJSON, _ := json.Marshal(cand.Nodes)
		groupsJSON, _ := json.Marshal(cand.Groups)
		depsJSON, _ := json.Marshal(cand.DependencyVersions)

		if err := tx.Model(&db.ConfigSnapshot{}).Where("active = ?", true).Update("active", false).Error; err != nil {
			return err
		}
		snap := db.ConfigSnapshot{
			CreatedAt:        time.Now(),
			Digest:           cand.Digest,
			ConfigID:         cand.ConfigModel.ID,
			ConfigVersion:    cand.ConfigModel.Version,
			DnsID:            cand.DnsModel.ID,
			DnsVersion:       cand.DnsModel.Version,
			RoutingID:        cand.RoutingModel.ID,
			RoutingVersion:   cand.RoutingModel.Version,
			GlobalText:       cand.GlobalText,
			DnsText:          cand.DnsText,
			RoutingText:      cand.RoutingText,
			NodesJSON:        string(nodesJSON),
			GroupsJSON:       string(groupsJSON),
			DependenciesJSON: string(depsJSON),
			Summary:          fmt.Sprintf("%s / %s / %s", cand.ConfigModel.Name, cand.DnsModel.Name, cand.RoutingModel.Name),
			Active:           true,
		}
		if err := tx.Create(&snap).Error; err != nil {
			return err
		}

		// Keep the latest 10 snapshots (always preserving at least current + previous).
		var oldSnaps []db.ConfigSnapshot
		if err := tx.Order("id desc").Offset(10).Find(&oldSnaps).Error; err == nil && len(oldSnaps) > 0 {
			var delIDs []uint
			for _, osnap := range oldSnaps {
				delIDs = append(delIDs, osnap.ID)
			}
			_ = tx.Where("id in ?", delIDs).Delete(&db.ConfigSnapshot{}).Error
		}

		var sys db.System
		if err := tx.Model(&db.System{}).FirstOrCreate(&sys).Error; err != nil {
			return err
		}
		var gvs uint
		var gids []string
		for _, g := range cand.GroupModels {
			gvs += g.Version
			gids = append(gids, fmt.Sprintf("%x", g.ID))
		}
		sort.Strings(gids)
		sid := snap.ID
		snapID = sid
		if err := tx.Model(&sys).Updates(map[string]interface{}{
			"running":                   true,
			"running_config_id":         cand.ConfigModel.ID,
			"running_config_version":    cand.ConfigModel.Version,
			"running_dns_id":            cand.DnsModel.ID,
			"running_dns_version":       cand.DnsModel.Version,
			"running_routing_id":        cand.RoutingModel.ID,
			"running_routing_version":   cand.RoutingModel.Version,
			"running_group_version_sum": gvs,
			"running_group_ids":         strings.Join(gids, ","),
			"active_snapshot_id":        &sid,
			"generation":                dae.ActiveGeneration(),
			"last_apply_status":         "running",
			"last_apply_error":          "",
			"active_digest":             cand.Digest,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&sys).Association("RunningGroups").Replace(cand.GroupModels); err != nil {
			return err
		}
		return nil
	}

	// If caller already opened a transaction (e.g., legacy MutationResolver.Run),
	// reuse it directly instead of starting a nested transaction.
	if _, inTx := d.Statement.ConnPool.(gorm.TxCommitter); inTx {
		if err := writeFn(d); err != nil {
			return 0, err
		}
		return snapID, nil
	}
	if err := d.Transaction(writeFn); err != nil {
		return 0, err
	}
	return snapID, nil
}

func buildDaeConfigFromSnapshot(snap *db.ConfigSnapshot) (*daeConfig.Config, error) {
	c, err := dae.ParseConfig(&snap.GlobalText, &snap.DnsText, &snap.RoutingText)
	if err != nil {
		return nil, err
	}
	var nodes []SnapshotNode
	if err := json.Unmarshal([]byte(snap.NodesJSON), &nodes); err != nil {
		return nil, err
	}
	var groups []SnapshotGroup
	if err := json.Unmarshal([]byte(snap.GroupsJSON), &groups); err != nil {
		return nil, err
	}
	for _, n := range nodes {
		c.Node = append(c.Node, daeConfig.KeyableString(fmt.Sprintf("%s:%s", n.UniqueName, n.Link)))
	}
	for _, g := range groups {
		var policy daeConfig.FunctionListOrString
		if len(g.PolicyParams) == 0 {
			policy = g.Policy
		} else {
			params := make([]*config_parser.Param, 0, len(g.PolicyParams))
			for _, p := range g.PolicyParams {
				params = append(params, &config_parser.Param{Key: p.Key, Val: p.Val})
			}
			policy = &config_parser.Function{Name: g.Policy, Params: params}
		}
		var nameParams []*config_parser.Param
		for _, mn := range g.MemberNames {
			nameParams = append(nameParams, &config_parser.Param{Val: mn})
		}
		c.Group = append(c.Group, daeConfig.Group{
			Name: g.Name,
			Filter: [][]*config_parser.Function{{{
				Name:   "name",
				Params: nameParams,
			}}},
			FilterAnnotation: [][]*config_parser.Param{{}},
			Policy:           policy,
		})
	}
	return c, nil
}

// RollbackPolicy restores a previously committed ConfigSnapshot directly using
// its frozen nodes, groups, Global, DNS, and Routing.
func RollbackPolicy(ctx context.Context, snapshotID uint) (*ApplyResult, error) {
	if ok := runLock.TryLock(); !ok {
		return nil, fmt.Errorf("another apply or reload operation is already in progress")
	}
	defer runLock.Unlock()

	d := db.DB(ctx)
	var snap db.ConfigSnapshot
	if err := d.Where("id = ?", snapshotID).First(&snap).Error; err != nil {
		return nil, fmt.Errorf("snapshot %d not found: %w", snapshotID, err)
	}
	daeConf, err := buildDaeConfigFromSnapshot(&snap)
	if err != nil {
		return nil, fmt.Errorf("build config from snapshot %d: %w", snapshotID, err)
	}

	chReload := make(chan error, 1)
	dae.ChReloadConfigs <- &dae.ReloadMessage{
		Config:              daeConf,
		RoutingTraceEnabled: true,
		Callback:            chReload,
	}
	if reloadErr := <-chReload; reloadErr != nil {
		return nil, fmt.Errorf("rollback reload failed: %w", reloadErr)
	}

	tx := d.Begin()
	defer tx.Rollback()
	_ = tx.Model(&db.ConfigSnapshot{}).Where("active = ?", true).Update("active", false).Error
	_ = tx.Model(&db.ConfigSnapshot{ID: snap.ID}).Update("active", true).Error
	var sys db.System
	if err := tx.Model(&db.System{}).FirstOrCreate(&sys).Error; err != nil {
		return nil, err
	}
	sid := snap.ID
	if err := tx.Model(&sys).Updates(map[string]interface{}{
		"running":            true,
		"active_snapshot_id": &sid,
		"generation":         dae.ActiveGeneration(),
		"last_apply_status":  "running",
		"last_apply_error":   "",
		"active_digest":      snap.Digest,
	}).Error; err != nil {
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	return &ApplyResult{
		Success:    true,
		Running:    true,
		RolledBack: true,
		SnapshotID: snap.ID,
		Digest:     snap.Digest,
		Generation: dae.ActiveGeneration(),
		Status:     "running",
		Message:    fmt.Sprintf("rolled back to snapshot %d", snap.ID),
	}, nil
}

// RestoreFromSnapshot restores the last committed ConfigSnapshot on daemon
// startup. If no snapshot exists yet (first boot of an upgraded legacy DB),
// it falls back to legacy Run(d, false), which creates the first snapshot.
func RestoreFromSnapshot(d *gorm.DB) error {
	var sys db.System
	if err := d.Model(&db.System{}).FirstOrCreate(&sys).Error; err != nil {
		return err
	}
	if !sys.Running {
		return nil
	}
	if sys.ActiveSnapshotID == nil || *sys.ActiveSnapshotID == 0 {
		// Legacy DB first boot: assemble from currently selected resources & create snapshot.
		_, err := Run(d, false)
		return err
	}
	if ok := runLock.TryLock(); !ok {
		return fmt.Errorf("restore conflict: runLock busy")
	}
	defer runLock.Unlock()

	var snap db.ConfigSnapshot
	if err := d.Where("id = ?", *sys.ActiveSnapshotID).First(&snap).Error; err != nil {
		// Fallback to legacy Run if snapshot row is missing.
		runLock.Unlock()
		_, runErr := Run(d, false)
		runLock.Lock()
		return runErr
	}
	daeConf, err := buildDaeConfigFromSnapshot(&snap)
	if err != nil {
		return err
	}
	chReload := make(chan error, 1)
	dae.ChReloadConfigs <- &dae.ReloadMessage{
		Config:              daeConf,
		RoutingTraceEnabled: true,
		Callback:            chReload,
	}
	if reloadErr := <-chReload; reloadErr != nil {
		_ = d.Model(&sys).Updates(map[string]interface{}{
			"running":           false,
			"last_apply_status": "apply_failed",
			"last_apply_error":  reloadErr.Error(),
		}).Error
		return reloadErr
	}
	return d.Model(&sys).Updates(map[string]interface{}{
		"running":           true,
		"generation":        dae.ActiveGeneration(),
		"last_apply_status": "running",
		"last_apply_error":  "",
	}).Error
}

// AutoApplyIfAllowed applies background updates (cron subscription / rule list)
// ONLY when:
//  1. Proxy is currently running,
//  2. Selected Config has AutoApply == true,
//  3. There are NO unapplied manual edits to Config/DNS/Routing, and
//  4. The assembled candidate digest actually changed.
func AutoApplyIfAllowed(d *gorm.DB, source string) {
	if !runLock.TryLock() {
		return
	}
	defer runLock.Unlock()

	var sys db.System
	if err := d.Model(&db.System{}).FirstOrCreate(&sys).Error; err != nil || !sys.Running {
		return
	}
	var mConfig db.Config
	if err := d.Where("selected = ?", true).First(&mConfig).Error; err != nil || !mConfig.AutoApply {
		return
	}
	var mDns db.Dns
	if err := d.Where("selected = ?", true).First(&mDns).Error; err != nil {
		return
	}
	var mRouting db.Routing
	if err := d.Where("selected = ?", true).First(&mRouting).Error; err != nil {
		return
	}
	// Do NOT auto-publish unapplied manual edits!
	if sys.RunningConfigID == nil || *sys.RunningConfigID != mConfig.ID || sys.RunningConfigVersion != mConfig.Version ||
		sys.RunningDnsID == nil || *sys.RunningDnsID != mDns.ID || sys.RunningDnsVersion != mDns.Version ||
		sys.RunningRoutingID == nil || *sys.RunningRoutingID != mRouting.ID || sys.RunningRoutingVersion != mRouting.Version {
		logrus.Infof("[AutoApply] skipped (%s): unapplied manual edits exist", source)
		return
	}

	cand, err := AssembleCandidate(d, AssembleOptions{ValidateDialers: true})
	if err != nil || cand.HasErrors() {
		logrus.Warnf("[AutoApply] skipped (%s): candidate invalid (%v)", source, err)
		return
	}
	if !cand.HasEffectiveChanges {
		logrus.Infof("[AutoApply] skipped (%s): no effective changes", source)
		return
	}
	if _, err := applyLocked(d, nil, &cand.Digest, AssembleOptions{}); err != nil {
		logrus.WithError(err).Warnf("[AutoApply] failed (%s)", source)
	} else {
		logrus.Infof("[AutoApply] applied (%s) digest=%s", source, shortDigest(cand.Digest))
	}
}

func ListSnapshots(ctx context.Context) ([]db.ConfigSnapshot, error) {
	var snaps []db.ConfigSnapshot
	if err := db.DB(ctx).Order("id desc").Limit(20).Find(&snaps).Error; err != nil {
		return nil, err
	}
	return snaps, nil
}

func SetAutoApply(ctx context.Context, configID uint, autoApply bool) (*db.Config, error) {
	var m db.Config
	if err := db.DB(ctx).Where("id = ?", configID).First(&m).Error; err != nil {
		return nil, err
	}
	if err := db.DB(ctx).Model(&m).Update("auto_apply", autoApply).Error; err != nil {
		return nil, err
	}
	m.AutoApply = autoApply
	return &m, nil
}

// Keep unused imports satisfied when needed.
var _ = os.ErrNotExist
var _ = strconv.Itoa
