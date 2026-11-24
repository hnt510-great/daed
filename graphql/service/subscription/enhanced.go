/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae-wing/graphql/service/rulelist"
	daeSub "github.com/daeuniverse/dae/common/subscription"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

type EnhancedSettingsInput struct {
	Format            *string
	UserAgent         *string
	DownloadMode      *string
	DownloadGroupName *string
	IncludeKeywords   *string
	ExcludeKeywords   *string
	FilterPriority    *string
	CronExp           *string
	CronEnable        *bool
	ExpectedRevision  *uint
}

type NodeParseError struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Param    string `json:"param"`
	Reason   string `json:"reason"`
}

type RefreshSummary struct {
	Subscription  *db.Subscription
	AddedCount    int
	UpdatedCount  int
	RemovedCount  int
	RebindCount   int
	FilteredCount int
	Errors        []NodeParseError
	Changed       bool
}

var (
	subRefreshLocksMu sync.Mutex
	subRefreshLocks   = make(map[uint]*sync.Mutex)

	subAutoApplyHookMu sync.RWMutex
	subAutoApplyHook   func(source string)
)

func RegisterAutoApplyHook(fn func(source string)) {
	subAutoApplyHookMu.Lock()
	defer subAutoApplyHookMu.Unlock()
	subAutoApplyHook = fn
}

func triggerSubAutoApply(source string) {
	subAutoApplyHookMu.RLock()
	fn := subAutoApplyHook
	subAutoApplyHookMu.RUnlock()
	if fn != nil {
		fn(source)
	}
}

func lockForSub(id uint) *sync.Mutex {
	subRefreshLocksMu.Lock()
	defer subRefreshLocksMu.Unlock()
	m, ok := subRefreshLocks[id]
	if !ok {
		m = &sync.Mutex{}
		subRefreshLocks[id] = m
	}
	return m
}

type UserInfo struct {
	Upload   int64
	Download int64
	Total    int64
	ExpireAt *time.Time
	Raw      string
}

func ParseSubscriptionUserinfo(hdr http.Header) UserInfo {
	var ui UserInfo
	if hdr == nil {
		return ui
	}
	raw := strings.TrimSpace(hdr.Get("Subscription-Userinfo"))
	if raw == "" {
		return ui
	}
	ui.Raw = raw
	for _, part := range strings.Split(raw, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		v, err := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "upload":
			ui.Upload = v
		case "download":
			ui.Download = v
		case "total":
			ui.Total = v
		case "expire":
			if v > 0 {
				t := time.Unix(v, 0)
				ui.ExpireAt = &t
			}
		}
	}
	return ui
}

// ComputeNodeKeys computes (StableKey, IdentityKey) for a proxy link.
// StableKey strips display remark (#name) and canonicalizes query order so
// renaming a node preserves its ID.
// IdentityKey strips credentials (user/password/uuid) and remark, keeping
// protocol + host:port + transport + sni/path so unique credential rotations
// can update in-place without mis-binding unrelated nodes.
func ComputeNodeKeys(link string) (stableKey string, identityKey string) {
	trimmed := strings.TrimSpace(link)
	// Handle vmess://base64json specially
	if strings.HasPrefix(strings.ToLower(trimmed), "vmess://") {
		b64 := strings.TrimSpace(trimmed[len("vmess://"):])
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(b64)
		}
		if err == nil {
			var m map[string]interface{}
			if json.Unmarshal(raw, &m) == nil {
				delete(m, "ps")
				sb, _ := json.Marshal(m)
				h1 := sha256.Sum256(sb)
				idParts := fmt.Sprintf("vmess|%v:%v|%v|%v|%v", m["add"], m["port"], m["net"], m["sni"], m["path"])
				h2 := sha256.Sum256([]byte(strings.ToLower(idParts)))
				return hex.EncodeToString(h1[:16]), hex.EncodeToString(h2[:16])
			}
		}
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		h := sha256.Sum256([]byte(trimmed))
		s := hex.EncodeToString(h[:16])
		return s, s
	}
	uNoFrag := *u
	uNoFrag.Fragment = ""
	// Canonicalize query params
	q := uNoFrag.Query()
	uNoFrag.RawQuery = q.Encode()
	hStable := sha256.Sum256([]byte(uNoFrag.String()))

	idStr := strings.ToLower(fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		u.Scheme,
		u.Host,
		q.Get("type"),
		q.Get("security"),
		q.Get("sni"),
		q.Get("path"),
	))
	hIdent := sha256.Sum256([]byte(idStr))
	return hex.EncodeToString(hStable[:16]), hex.EncodeToString(hIdent[:16])
}

func splitKeywords(raw string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';'
	}) {
		tok = strings.TrimSpace(tok)
		if tok != "" {
			out = append(out, strings.ToLower(tok))
		}
	}
	return out
}

func matchesKeywordFilter(name string, includeRaw, excludeRaw, priority string) bool {
	lower := strings.ToLower(name)
	includes := splitKeywords(includeRaw)
	excludes := splitKeywords(excludeRaw)

	inMatched := len(includes) == 0
	for _, kw := range includes {
		if strings.Contains(lower, kw) {
			inMatched = true
			break
		}
	}
	exMatched := false
	for _, kw := range excludes {
		if strings.Contains(lower, kw) {
			exMatched = true
			break
		}
	}

	if priority == "include_first" {
		if len(includes) > 0 && inMatched {
			return true
		}
		return !exMatched && inMatched
	}
	// Default: exclude_first
	if exMatched {
		return false
	}
	return inMatched
}

type clashConfig struct {
	Proxies []map[string]interface{} `yaml:"proxies"`
}

func strField(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch tv := v.(type) {
			case string:
				return tv
			case int:
				return strconv.Itoa(tv)
			case int64:
				return strconv.FormatInt(tv, 10)
			case float64:
				return strconv.FormatInt(int64(tv), 10)
			case bool:
				return strconv.FormatBool(tv)
			}
		}
	}
	return ""
}

func boolField(m map[string]interface{}, keys ...string) bool {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

func mapField(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key]; ok && v != nil {
		if mm, ok := v.(map[string]interface{}); ok {
			return mm
		}
	}
	return nil
}

// ConvertClashProxy converts a single Clash YAML proxy map to a dae outbound link.
func ConvertClashProxy(idx int, p map[string]interface{}) (string, *NodeParseError) {
	ptype := strings.ToLower(strings.TrimSpace(strField(p, "type")))
	name := strings.TrimSpace(strField(p, "name"))
	server := strings.TrimSpace(strField(p, "server"))
	port := strings.TrimSpace(strField(p, "port"))
	if ptype == "" || server == "" || port == "" {
		return "", &NodeParseError{Index: idx, Name: name, Protocol: ptype, Param: "server/port", Reason: "missing type, server, or port"}
	}
	netType := strings.ToLower(strings.TrimSpace(strField(p, "network")))
	if netType == "xhttp" || netType == "mkcp" || netType == "kcp" {
		return "", &NodeParseError{Index: idx, Name: name, Protocol: ptype, Param: "network", Reason: fmt.Sprintf("transport %q is not supported by daed", netType)}
	}

	frag := url.PathEscape(name)
	hostPort := server + ":" + port
	if strings.Contains(server, ":") && !strings.HasPrefix(server, "[") {
		hostPort = "[" + server + "]:" + port
	}

	switch ptype {
	case "wireguard":
		return "", &NodeParseError{Index: idx, Name: name, Protocol: "wireguard", Param: "type", Reason: "wireguard node is outside supported outbound protocols"}
	case "socks5":
		u := url.URL{Scheme: "socks5", Host: hostPort, Fragment: name}
		if user := strField(p, "username"); user != "" {
			u.User = url.UserPassword(user, strField(p, "password"))
		}
		return u.String(), nil
	case "http":
		scheme := "http"
		if boolField(p, "tls") {
			scheme = "https"
		}
		u := url.URL{Scheme: scheme, Host: hostPort, Fragment: name}
		if user := strField(p, "username"); user != "" {
			u.User = url.UserPassword(user, strField(p, "password"))
		}
		return u.String(), nil
	case "ss":
		cipher := strField(p, "cipher")
		password := strField(p, "password")
		if cipher == "" || password == "" {
			return "", &NodeParseError{Index: idx, Name: name, Protocol: "ss", Param: "cipher/password", Reason: "missing cipher or password"}
		}
		cred := base64.RawURLEncoding.EncodeToString([]byte(cipher + ":" + password))
		return fmt.Sprintf("ss://%s@%s#%s", cred, hostPort, frag), nil
	case "trojan":
		password := strField(p, "password")
		if password == "" {
			return "", &NodeParseError{Index: idx, Name: name, Protocol: "trojan", Param: "password", Reason: "missing password"}
		}
		q := url.Values{}
		if sni := strField(p, "sni"); sni != "" {
			q.Set("sni", sni)
		}
		if boolField(p, "skip-cert-verify") {
			q.Set("allowInsecure", "1")
		}
		if netType != "" {
			q.Set("type", netType)
		}
		if ws := mapField(p, "ws-opts"); ws != nil {
			if path := strField(ws, "path"); path != "" {
				q.Set("path", path)
			}
			if hdrs := mapField(ws, "headers"); hdrs != nil {
				if h := strField(hdrs, "Host", "host"); h != "" {
					q.Set("host", h)
				}
			}
		}
		u := url.URL{Scheme: "trojan", User: url.User(password), Host: hostPort, RawQuery: q.Encode(), Fragment: name}
		return u.String(), nil
	case "vless":
		uuid := strField(p, "uuid")
		if uuid == "" {
			return "", &NodeParseError{Index: idx, Name: name, Protocol: "vless", Param: "uuid", Reason: "missing uuid"}
		}
		q := url.Values{}
		q.Set("encryption", "none")
		if netType == "" {
			netType = "tcp"
		}
		q.Set("type", netType)
		if flow := strField(p, "flow"); flow != "" {
			q.Set("flow", flow)
		}
		if ro := mapField(p, "reality-opts"); ro != nil {
			q.Set("security", "reality")
			if pbk := strField(ro, "public-key"); pbk != "" {
				q.Set("pbk", pbk)
			}
			if sid := strField(ro, "short-id"); sid != "" {
				q.Set("sid", sid)
			}
		} else if boolField(p, "tls") {
			q.Set("security", "tls")
		} else {
			q.Set("security", "none")
		}
		if sni := strField(p, "servername", "sni"); sni != "" {
			q.Set("sni", sni)
		}
		if fp := strField(p, "client-fingerprint", "fingerprint"); fp != "" {
			q.Set("fp", fp)
		}
		if boolField(p, "skip-cert-verify") {
			q.Set("allowInsecure", "1")
		}
		if ws := mapField(p, "ws-opts"); ws != nil {
			if path := strField(ws, "path"); path != "" {
				q.Set("path", path)
			}
			if hdrs := mapField(ws, "headers"); hdrs != nil {
				if h := strField(hdrs, "Host", "host"); h != "" {
					q.Set("host", h)
				}
			}
		}
		if grpc := mapField(p, "grpc-opts"); grpc != nil {
			if sn := strField(grpc, "grpc-service-name"); sn != "" {
				q.Set("serviceName", sn)
			}
		}
		u := url.URL{Scheme: "vless", User: url.User(uuid), Host: hostPort, RawQuery: q.Encode(), Fragment: name}
		return u.String(), nil
	case "vmess":
		uuid := strField(p, "uuid")
		if uuid == "" {
			return "", &NodeParseError{Index: idx, Name: name, Protocol: "vmess", Param: "uuid", Reason: "missing uuid"}
		}
		if netType == "" {
			netType = "tcp"
		}
		vm := map[string]interface{}{
			"v":    "2",
			"ps":   name,
			"add":  server,
			"port": port,
			"id":   uuid,
			"aid":  strField(p, "alterId"),
			"scy":  strField(p, "cipher"),
			"net":  netType,
			"type": "none",
		}
		if boolField(p, "tls") {
			vm["tls"] = "tls"
		}
		if sni := strField(p, "servername", "sni"); sni != "" {
			vm["sni"] = sni
		}
		if ws := mapField(p, "ws-opts"); ws != nil {
			vm["path"] = strField(ws, "path")
			if hdrs := mapField(ws, "headers"); hdrs != nil {
				vm["host"] = strField(hdrs, "Host", "host")
			}
		}
		b, _ := json.Marshal(vm)
		return "vmess://" + base64.StdEncoding.EncodeToString(b), nil
	case "hysteria2":
		password := strField(p, "password")
		if password == "" {
			return "", &NodeParseError{Index: idx, Name: name, Protocol: "hysteria2", Param: "password", Reason: "missing password"}
		}
		q := url.Values{}
		if sni := strField(p, "sni"); sni != "" {
			q.Set("sni", sni)
		}
		if boolField(p, "skip-cert-verify") {
			q.Set("insecure", "1")
		}
		if obfs := strField(p, "obfs"); obfs != "" {
			q.Set("obfs", obfs)
			if op := strField(p, "obfs-password"); op != "" {
				q.Set("obfs-password", op)
			}
		}
		u := url.URL{Scheme: "hysteria2", User: url.User(password), Host: hostPort, RawQuery: q.Encode(), Fragment: name}
		return u.String(), nil
	case "tuic":
		uuid := strField(p, "uuid")
		password := strField(p, "password")
		if uuid == "" {
			return "", &NodeParseError{Index: idx, Name: name, Protocol: "tuic", Param: "uuid", Reason: "missing uuid"}
		}
		q := url.Values{}
		if sni := strField(p, "sni"); sni != "" {
			q.Set("sni", sni)
		}
		if cc := strField(p, "congestion-controller"); cc != "" {
			q.Set("congestion_control", cc)
		}
		if boolField(p, "skip-cert-verify") {
			q.Set("allow_insecure", "1")
		}
		u := url.URL{Scheme: "tuic", User: url.UserPassword(uuid, password), Host: hostPort, RawQuery: q.Encode(), Fragment: name}
		return u.String(), nil
	default:
		return "", &NodeParseError{Index: idx, Name: name, Protocol: ptype, Param: "type", Reason: fmt.Sprintf("unsupported Clash proxy type %q", ptype)}
	}
}

// ParseSubscriptionPayload detects format (SIP008, Clash YAML, Base64/plain links)
// and converts entries into candidate node links plus sanitized per-entry errors.
func ParseSubscriptionPayload(raw string, format string) (links []string, errs []NodeParseError, detectedFormat string, fatalErr error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil, "", fmt.Errorf("subscription response is empty")
	}
	if rulelist.IsLikelyHTML(raw) {
		return nil, nil, "", fmt.Errorf("subscription response appears to be an HTML error page")
	}

	noLogger := logrus.New()
	noLogger.SetOutput(io.Discard)

	tryClash := func() bool {
		if !strings.Contains(raw, "proxies:") {
			return false
		}
		var cc clashConfig
		if err := yaml.Unmarshal([]byte(raw), &cc); err != nil || len(cc.Proxies) == 0 {
			return false
		}
		detectedFormat = "clash_yaml"
		for i, p := range cc.Proxies {
			lnk, perr := ConvertClashProxy(i+1, p)
			if perr != nil {
				errs = append(errs, *perr)
				continue
			}
			links = append(links, lnk)
		}
		return true
	}

	switch strings.ToLower(strings.TrimSpace(format)) {
	case "clash_yaml":
		if !tryClash() {
			return nil, nil, "clash_yaml", fmt.Errorf("failed to parse Clash YAML proxies")
		}
		return links, errs, detectedFormat, nil
	case "sip008":
		ls, err := daeSub.ResolveSubscriptionAsSIP008(noLogger, []byte(raw))
		if err != nil {
			return nil, nil, "sip008", err
		}
		return ls, nil, "sip008", nil
	case "base64":
		ls := daeSub.ResolveSubscriptionAsBase64(noLogger, []byte(raw))
		return ls, nil, "base64", nil
	default: // auto
		if tryClash() {
			return links, errs, detectedFormat, nil
		}
		if ls, err := daeSub.ResolveSubscriptionAsSIP008(noLogger, []byte(raw)); err == nil && len(ls) > 0 {
			return ls, nil, "sip008", nil
		}
		ls := daeSub.ResolveSubscriptionAsBase64(noLogger, []byte(raw))
		if len(ls) > 0 {
			return ls, nil, "base64", nil
		}
		return nil, nil, "auto", fmt.Errorf("no valid nodes found in subscription payload")
	}
}

// UpdateEnhancedSettings updates format, UA, download mode/group, keyword filters, and cron.
func UpdateEnhancedSettings(ctx context.Context, subID uint, in EnhancedSettingsInput) (*db.Subscription, error) {
	tx := db.BeginTx(ctx)
	defer tx.Rollback()

	var m db.Subscription
	if err := tx.Where("id = ?", subID).First(&m).Error; err != nil {
		return nil, err
	}
	if in.ExpectedRevision != nil && m.Revision != *in.ExpectedRevision {
		return nil, fmt.Errorf("subscription revision conflict: current %d, expected %d", m.Revision, *in.ExpectedRevision)
	}
	if in.Format != nil {
		m.Format = strings.TrimSpace(*in.Format)
	}
	if in.UserAgent != nil {
		m.UserAgent = strings.TrimSpace(*in.UserAgent)
	}
	if in.DownloadMode != nil {
		m.DownloadMode = strings.TrimSpace(*in.DownloadMode)
	}
	if in.DownloadGroupName != nil {
		m.DownloadGroupName = strings.TrimSpace(*in.DownloadGroupName)
	}
	if in.IncludeKeywords != nil {
		m.IncludeKeywords = *in.IncludeKeywords
	}
	if in.ExcludeKeywords != nil {
		m.ExcludeKeywords = *in.ExcludeKeywords
	}
	if in.FilterPriority != nil {
		m.FilterPriority = strings.TrimSpace(*in.FilterPriority)
	}
	cronChanged := false
	if in.CronExp != nil {
		m.CronExp = strings.TrimSpace(*in.CronExp)
		cronChanged = true
	}
	if in.CronEnable != nil {
		m.CronEnable = *in.CronEnable
		cronChanged = true
	}
	m.Revision++
	if err := tx.Save(&m).Error; err != nil {
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	if cronChanged {
		RemoveUpdateScheduler(subID)
		if m.CronEnable {
			AddUpdateScheduler(ctx, subID)
		}
	}
	return &m, nil
}

// RefreshStableByID performs enhanced fetch, parse, dialer validation, keyword
// filtering, and stable-ID reconciliation (in-place update on rename or unique
// credential rotation; mark referenced missing/ambiguous nodes as NeedsRebind).
func RefreshStableByID(ctx context.Context, subID uint, fromCron bool) (*RefreshSummary, error) {
	mu := lockForSub(subID)
	mu.Lock()
	defer mu.Unlock()

	var sub db.Subscription
	if err := db.DB(ctx).Where("id = ?", subID).First(&sub).Error; err != nil {
		return nil, err
	}
	origRevision := sub.Revision

	// 1. Network fetch strictly outside DB transaction.
	ua := sub.UserAgent
	if ua == "" {
		ua = fmt.Sprintf("%v/%v (like v2rayA/1.0 WebRequestHelper) (like v2rayN/1.0 WebRequestHelper)", db.AppName, db.AppVersion)
	}
	body, hdr, fetchErr := rulelist.FetchRemoteContent(ctx, sub.Link, sub.DownloadMode, sub.DownloadGroupName, ua)
	if fetchErr != nil {
		_ = db.DB(ctx).Model(&db.Subscription{ID: subID}).Update("last_error", fetchErr.Error()).Error
		return nil, fetchErr
	}
	userInfo := ParseSubscriptionUserinfo(hdr)

	// 2. Parse payload into links.
	rawLinks, parseErrs, _, fatalErr := ParseSubscriptionPayload(body, sub.Format)
	if fatalErr != nil {
		_ = db.DB(ctx).Model(&db.Subscription{ID: subID}).Update("last_error", fatalErr.Error()).Error
		return nil, fatalErr
	}

	// 3. Validate dialers & apply keyword filter.
	type parsedCandidate struct {
		model       *db.Node
		stableKey   string
		identityKey string
	}
	var candidates []parsedCandidate
	filteredOut := 0
	for idx, lnk := range rawLinks {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lnk)), "naive+quic://") {
			parseErrs = append(parseErrs, NodeParseError{
				Index:    idx + 1,
				Protocol: "naive+quic",
				Param:    "transport",
				Reason:   "naive+quic transport is not supported in this version; use naive+https",
			})
			continue
		}
		nm, err := db.NewNodeModel(lnk, nil, &subID)
		if err != nil {
			proto := ""
			if ci := strings.Index(lnk, "://"); ci > 0 {
				proto = lnk[:ci]
			}
			parseErrs = append(parseErrs, NodeParseError{
				Index:    idx + 1,
				Protocol: proto,
				Param:    "dialer",
				Reason:   sanitizeDialerError(err.Error()),
			})
			continue
		}
		if !matchesKeywordFilter(nm.Name, sub.IncludeKeywords, sub.ExcludeKeywords, sub.FilterPriority) {
			filteredOut++
			continue
		}
		sk, ik := ComputeNodeKeys(lnk)
		nm.StableKey = sk
		nm.IdentityKey = ik
		candidates = append(candidates, parsedCandidate{
			model:       nm,
			stableKey:   sk,
			identityKey: ik,
		})
	}

	if len(candidates) == 0 {
		err := fmt.Errorf("subscription refresh aborted: 0 valid nodes after parsing/filtering (keeping previous node set)")
		_ = db.DB(ctx).Model(&db.Subscription{ID: subID}).Update("last_error", err.Error()).Error
		return &RefreshSummary{
			Subscription:  &sub,
			FilteredCount: filteredOut,
			Errors:        parseErrs,
		}, err
	}

	// 4. Transactional stable-ID reconciliation.
	tx := db.BeginTx(ctx)
	defer tx.Rollback()

	var curSub db.Subscription
	if err := tx.Where("id = ?", subID).First(&curSub).Error; err != nil {
		return nil, err
	}
	if curSub.Revision != origRevision {
		return nil, fmt.Errorf("subscription modified concurrently during refresh (revision %d != %d)", curSub.Revision, origRevision)
	}

	var existing []db.Node
	if err := tx.Where("subscription_id = ?", subID).Order("id asc").Find(&existing).Error; err != nil {
		return nil, err
	}

	// Ensure existing nodes have StableKey/IdentityKey populated (e.g. upgraded legacy nodes).
	for i := range existing {
		if existing[i].StableKey == "" || existing[i].IdentityKey == "" {
			existing[i].StableKey, existing[i].IdentityKey = ComputeNodeKeys(existing[i].Link)
		}
	}

	// Determine which existing nodes are explicitly referenced by groups, chains, or failover.
	referencedIDs := findReferencedNodeIDs(tx, subID)

	matchedOld := make(map[uint]bool)
	matchedCand := make(map[int]bool)
	summary := &RefreshSummary{
		FilteredCount: filteredOut,
		Errors:        parseErrs,
	}

	// Pass A: Exact StableKey match (handles unchanged links & renames in-place!).
	oldByStable := make(map[string][]int)
	for i, n := range existing {
		oldByStable[n.StableKey] = append(oldByStable[n.StableKey], i)
	}
	for ci, cand := range candidates {
		idxs := oldByStable[cand.stableKey]
		for _, oi := range idxs {
			if matchedOld[existing[oi].ID] {
				continue
			}
			oldNode := &existing[oi]
			matchedOld[oldNode.ID] = true
			matchedCand[ci] = true
			if oldNode.Link != cand.model.Link || oldNode.Name != cand.model.Name || oldNode.NeedsRebind {
				oldNode.Link = cand.model.Link
				oldNode.Name = cand.model.Name
				oldNode.Address = cand.model.Address
				oldNode.Protocol = cand.model.Protocol
				oldNode.StableKey = cand.stableKey
				oldNode.IdentityKey = cand.identityKey
				oldNode.NeedsRebind = false
				oldNode.RebindReason = ""
				if err := tx.Save(oldNode).Error; err != nil {
					return nil, err
				}
				summary.UpdatedCount++
				summary.Changed = true
			}
			break
		}
	}

	// Pass B: Unique IdentityKey match for credential rotation (ONLY when 1 unmatched old <-> 1 unmatched cand).
	unmatchedOldByIdent := make(map[string][]int)
	for i, n := range existing {
		if !matchedOld[n.ID] {
			unmatchedOldByIdent[n.IdentityKey] = append(unmatchedOldByIdent[n.IdentityKey], i)
		}
	}
	unmatchedCandByIdent := make(map[string][]int)
	for ci, cand := range candidates {
		if !matchedCand[ci] {
			unmatchedCandByIdent[cand.identityKey] = append(unmatchedCandByIdent[cand.identityKey], ci)
		}
	}
	for ik, oldIdxs := range unmatchedOldByIdent {
		candIdxs := unmatchedCandByIdent[ik]
		if len(oldIdxs) == 1 && len(candIdxs) == 1 {
			oldNode := &existing[oldIdxs[0]]
			cand := candidates[candIdxs[0]]
			matchedOld[oldNode.ID] = true
			matchedCand[candIdxs[0]] = true
			oldNode.Link = cand.model.Link
			oldNode.Name = cand.model.Name
			oldNode.Address = cand.model.Address
			oldNode.Protocol = cand.model.Protocol
			oldNode.StableKey = cand.stableKey
			oldNode.IdentityKey = cand.identityKey
			oldNode.NeedsRebind = false
			oldNode.RebindReason = ""
			if err := tx.Save(oldNode).Error; err != nil {
				return nil, err
			}
			summary.UpdatedCount++
			summary.Changed = true
		} else if len(candIdxs) > 1 {
			// Ambiguous credential rotation: mark referenced old nodes as NeedsRebind!
			for _, oi := range oldIdxs {
				oldNode := &existing[oi]
				if referencedIDs[oldNode.ID] {
					matchedOld[oldNode.ID] = true
					oldNode.NeedsRebind = true
					oldNode.RebindReason = "ambiguous node update during subscription refresh; please rebind"
					if err := tx.Save(oldNode).Error; err != nil {
						return nil, err
					}
					markChainsNeedingRebind(tx, oldNode.ID, oldNode.RebindReason)
					summary.RebindCount++
					summary.Changed = true
				}
			}
		}
	}

	// Pass C: Handle remaining unmatched old nodes.
	for i := range existing {
		oldNode := &existing[i]
		if matchedOld[oldNode.ID] {
			continue
		}
		if referencedIDs[oldNode.ID] {
			// Keep row with NeedsRebind so group/chain/failover references are visibly flagged instead of silently broken.
			oldNode.NeedsRebind = true
			oldNode.RebindReason = "node removed or filtered out in latest subscription refresh"
			if err := tx.Save(oldNode).Error; err != nil {
				return nil, err
			}
			markChainsNeedingRebind(tx, oldNode.ID, oldNode.RebindReason)
			summary.RebindCount++
			summary.Changed = true
		} else {
			if err := tx.Delete(oldNode).Error; err != nil {
				return nil, err
			}
			summary.RemovedCount++
			summary.Changed = true
		}
	}

	// Pass D: Insert remaining unmatched new candidates.
	for ci, cand := range candidates {
		if matchedCand[ci] {
			continue
		}
		if err := tx.Create(cand.model).Error; err != nil {
			return nil, err
		}
		summary.AddedCount++
		summary.Changed = true
	}

	// Update subscription metadata & diff summary.
	curSub.UpdatedAt = time.Now()
	curSub.LastError = ""
	if userInfo.Raw != "" {
		curSub.Info = userInfo.Raw
		curSub.TrafficUpload = userInfo.Upload
		curSub.TrafficDownload = userInfo.Download
		curSub.TrafficTotal = userInfo.Total
		curSub.ExpireAt = userInfo.ExpireAt
	}
	curSub.LastDiffSummary = fmt.Sprintf("+%d ~%d -%d rebind:%d filtered:%d errors:%d",
		summary.AddedCount, summary.UpdatedCount, summary.RemovedCount,
		summary.RebindCount, summary.FilteredCount, len(summary.Errors))
	if summary.Changed {
		curSub.Revision++
	}
	if err := tx.Save(&curSub).Error; err != nil {
		return nil, err
	}
	if summary.Changed {
		if err := AutoUpdateVersionByIds(tx, []uint{subID}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}

	summary.Subscription = &curSub
	if summary.Changed && fromCron {
		go triggerSubAutoApply(fmt.Sprintf("subscription:%d", subID))
	}
	return summary, nil
}

func findReferencedNodeIDs(tx *gorm.DB, subID uint) map[uint]bool {
	refs := make(map[uint]bool)
	// 1. Explicit group_nodes
	var gnIDs []uint
	_ = tx.Raw(`SELECT gn.node_id FROM group_nodes gn INNER JOIN nodes n ON n.id = gn.node_id WHERE n.subscription_id = ?`, subID).Scan(&gnIDs).Error
	for _, id := range gnIDs {
		refs[id] = true
	}
	// 2. ProxyChains
	var chains []db.ProxyChain
	if err := tx.Find(&chains).Error; err == nil {
		for _, ch := range chains {
			var hops []uint
			if json.Unmarshal([]byte(ch.MemberNodeIDsJSON), &hops) == nil {
				for _, hid := range hops {
					refs[hid] = true
				}
			}
		}
	}
	// 3. GroupFailover configs
	var fcs []db.GroupFailover
	if err := tx.Find(&fcs).Error; err == nil {
		addKey := func(k string) {
			if strings.HasPrefix(k, "node:") {
				if id, err := strconv.ParseUint(strings.TrimPrefix(k, "node:"), 10, 64); err == nil {
					refs[uint(id)] = true
				}
			}
		}
		for _, fc := range fcs {
			addKey(fc.PrimaryMemberKey)
			var backups []string
			if json.Unmarshal([]byte(fc.BackupMemberKeysJSON), &backups) == nil {
				for _, b := range backups {
					addKey(b)
				}
			}
		}
	}
	return refs
}

func markChainsNeedingRebind(tx *gorm.DB, nodeID uint, reason string) {
	var chains []db.ProxyChain
	if err := tx.Find(&chains).Error; err != nil {
		return
	}
	for i := range chains {
		var hops []uint
		if json.Unmarshal([]byte(chains[i].MemberNodeIDsJSON), &hops) != nil {
			continue
		}
		for _, hid := range hops {
			if hid == nodeID {
				_ = tx.Model(&db.ProxyChain{ID: chains[i].ID}).Updates(map[string]interface{}{
					"needs_rebind":  true,
					"rebind_reason": fmt.Sprintf("hop node %d: %s", nodeID, reason),
					"version":       gorm.Expr("version + 1"),
				}).Error
				break
			}
		}
	}
}

func sanitizeDialerError(msg string) string {
	// Never echo full proxy URI credentials in error messages.
	if idx := strings.Index(msg, "://"); idx >= 0 {
		prefix := msg[:idx]
		return prefix + ": invalid node configuration (URI redacted)"
	}
	return msg
}

// Keep sort import used.
var _ = sort.Strings
