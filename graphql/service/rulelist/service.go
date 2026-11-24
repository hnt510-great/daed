/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package rulelist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/daeuniverse/dae-wing/dae"
	"github.com/daeuniverse/dae-wing/db"
	"github.com/go-co-op/gocron"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const maxRuleListBytes = 16 << 20 // 16 MiB

type SaveInput struct {
	ID                *uint
	Name              string
	Kind              string
	TargetGroup       string
	SourceType        string // static | remote
	URL               string
	DownloadMode      string // auto | direct | group
	DownloadGroupName string
	UserAgent         string
	CronExp           string
	CronEnable        bool
	RawText           string
	ExpectedRevision  *uint
}

type ReferenceInfo struct {
	ObjectType string `json:"objectType"` // routing | dns
	ObjectID   uint   `json:"objectId"`
	ObjectName string `json:"objectName"`
}

type RefreshResult struct {
	RuleList     *db.RuleList
	Changed      bool
	AddedCount   int
	RemovedCount int
	Diagnostics  []ParseDiagnostic
}

var (
	// Per-source serialization locks so manual and cron refreshes never race.
	sourceLocksMu sync.Mutex
	sourceLocks   = make(map[uint]*sync.Mutex)

	schedulerMu    sync.RWMutex
	schedulerCache = make(map[uint]*gocron.Scheduler)

	// Optional callback registered by config package for opt-in auto-apply.
	autoApplyHookMu sync.RWMutex
	autoApplyHook   func(source string)
)

func RegisterAutoApplyHook(fn func(source string)) {
	autoApplyHookMu.Lock()
	defer autoApplyHookMu.Unlock()
	autoApplyHook = fn
}

func triggerAutoApply(source string) {
	autoApplyHookMu.RLock()
	fn := autoApplyHook
	autoApplyHookMu.RUnlock()
	if fn != nil {
		fn(source)
	}
}

func lockForList(id uint) *sync.Mutex {
	sourceLocksMu.Lock()
	defer sourceLocksMu.Unlock()
	m, ok := sourceLocks[id]
	if !ok {
		m = &sync.Mutex{}
		sourceLocks[id] = m
	}
	return m
}

func contentDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func ListAll(ctx context.Context) ([]db.RuleList, error) {
	var lists []db.RuleList
	if err := db.DB(ctx).Order("id asc").Find(&lists).Error; err != nil {
		return nil, err
	}
	return lists, nil
}

func FindReferences(d *gorm.DB, listID uint) ([]ReferenceInfo, error) {
	var refs []ReferenceInfo
	// Check visual routings referencing this listID.
	var routings []db.Routing
	if err := d.Find(&routings).Error; err == nil {
		needle := fmt.Sprintf(`"listId":%d`, listID)
		needleSpaced := fmt.Sprintf(`"listId": %d`, listID)
		for _, r := range routings {
			if strings.Contains(r.VisualPolicyJSON, needle) || strings.Contains(r.VisualPolicyJSON, needleSpaced) {
				refs = append(refs, ReferenceInfo{
					ObjectType: "routing",
					ObjectID:   r.ID,
					ObjectName: r.Name,
				})
			}
		}
	}
	// Check DNS configs referencing this listID.
	var dnss []db.Dns
	if err := d.Find(&dnss).Error; err == nil {
		var bindings []struct {
			ListID uint `json:"listId"`
		}
		for _, dn := range dnss {
			if strings.TrimSpace(dn.DomainListRefsJSON) == "" {
				continue
			}
			bindings = nil
			if err := json.Unmarshal([]byte(dn.DomainListRefsJSON), &bindings); err == nil {
				for _, b := range bindings {
					if b.ListID == listID {
						refs = append(refs, ReferenceInfo{
							ObjectType: "dns",
							ObjectID:   dn.ID,
							ObjectName: dn.Name,
						})
						break
					}
				}
			}
		}
	}
	return refs, nil
}

func Save(ctx context.Context, in SaveInput) (*db.RuleList, []ParseDiagnostic, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, nil, fmt.Errorf("rule list name is required")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = "domain"
	}
	sourceType := strings.TrimSpace(in.SourceType)
	if sourceType == "" {
		sourceType = "static"
	}
	downloadMode := strings.TrimSpace(in.DownloadMode)
	if downloadMode == "" {
		downloadMode = "auto"
	}
	if in.CronEnable {
		if strings.TrimSpace(in.CronExp) == "" {
			return nil, nil, fmt.Errorf("cron expression is required when enabling scheduled updates")
		}
		s := gocron.NewScheduler(time.Local)
		_, err := s.Cron(in.CronExp).Do(func() {})
		s.Stop()
		if err != nil {
			return nil, nil, fmt.Errorf("invalid cron expression %q: %w", in.CronExp, err)
		}
	}

	var parsed *ParseResult
	if sourceType == "static" {
		parsed = ParseRuleList(in.RawText, kind)
	}

	tx := db.BeginTx(ctx)
	defer tx.Rollback()

	var m db.RuleList
	if in.ID != nil && *in.ID > 0 {
		if err := tx.Where("id = ?", *in.ID).First(&m).Error; err != nil {
			return nil, nil, err
		}
		if in.ExpectedRevision != nil && m.Revision != *in.ExpectedRevision {
			return nil, nil, fmt.Errorf("revision conflict: rule list %q is at revision %d, expected %d", m.Name, m.Revision, *in.ExpectedRevision)
		}
		oldContent := m.LastContent
		m.Name = name
		m.Kind = kind
		m.TargetGroup = strings.TrimSpace(in.TargetGroup)
		m.SourceType = sourceType
		m.URL = strings.TrimSpace(in.URL)
		m.DownloadMode = downloadMode
		m.DownloadGroupName = strings.TrimSpace(in.DownloadGroupName)
		m.UserAgent = strings.TrimSpace(in.UserAgent)
		m.CronExp = strings.TrimSpace(in.CronExp)
		m.CronEnable = in.CronEnable
		m.RawText = in.RawText
		m.Revision++
		if sourceType == "static" && parsed != nil {
			if !parsed.HasErrors {
				now := time.Now()
				added, removed := DiffEntryCounts(oldContent, in.RawText, kind)
				m.LastContent = in.RawText
				m.LastDigest = contentDigest(in.RawText)
				m.LastEntryCount = len(parsed.Entries)
				m.LastAddedCount = added
				m.LastRemovedCount = removed
				m.LastStatus = "ready"
				m.LastError = ""
				m.LastUpdatedAt = &now
				m.Version++
			} else {
				m.LastStatus = "error"
				if len(parsed.Diagnostics) > 0 {
					m.LastError = parsed.Diagnostics[0].Message
				}
			}
		}
		if err := tx.Save(&m).Error; err != nil {
			return nil, nil, err
		}
	} else {
		now := time.Now()
		m = db.RuleList{
			Name:              name,
			Kind:              kind,
			TargetGroup:       strings.TrimSpace(in.TargetGroup),
			SourceType:        sourceType,
			URL:               strings.TrimSpace(in.URL),
			DownloadMode:      downloadMode,
			DownloadGroupName: strings.TrimSpace(in.DownloadGroupName),
			UserAgent:         strings.TrimSpace(in.UserAgent),
			CronExp:           strings.TrimSpace(in.CronExp),
			CronEnable:        in.CronEnable,
			RawText:           in.RawText,
			Revision:          1,
			Version:           1,
		}
		if sourceType == "static" && parsed != nil {
			if !parsed.HasErrors {
				m.LastContent = in.RawText
				m.LastDigest = contentDigest(in.RawText)
				m.LastEntryCount = len(parsed.Entries)
				m.LastAddedCount = len(parsed.Entries)
				m.LastStatus = "ready"
				m.LastUpdatedAt = &now
			} else {
				m.LastStatus = "error"
				if len(parsed.Diagnostics) > 0 {
					m.LastError = parsed.Diagnostics[0].Message
				}
			}
		} else {
			m.LastStatus = "pending"
		}
		if err := tx.Create(&m).Error; err != nil {
			return nil, nil, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return nil, nil, err
	}
	SyncScheduler(ctx, m.ID)
	var diags []ParseDiagnostic
	if parsed != nil {
		diags = parsed.Diagnostics
	}
	return &m, diags, nil
}

func Delete(ctx context.Context, id uint) error {
	tx := db.BeginTx(ctx)
	defer tx.Rollback()
	refs, err := FindReferences(tx, id)
	if err != nil {
		return err
	}
	if len(refs) > 0 {
		var names []string
		for _, r := range refs {
			names = append(names, fmt.Sprintf("%s:%s", r.ObjectType, r.ObjectName))
		}
		return fmt.Errorf("cannot delete rule list: still referenced by %s", strings.Join(names, ", "))
	}
	if err := tx.Delete(&db.RuleList{ID: id}).Error; err != nil {
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	RemoveScheduler(id)
	return nil
}

// FetchRemoteContent fetches a URL honoring downloadMode (auto/direct/group) and User-Agent.
func FetchRemoteContent(ctx context.Context, rawURL, downloadMode, groupName, userAgent string) (string, http.Header, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", nil, fmt.Errorf("empty remote URL")
	}
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		ua = fmt.Sprintf("%v/%v", db.AppName, db.AppVersion)
	}
	doFetch := func(transport http.RoundTripper, timeout time.Duration) (string, http.Header, error) {
		client := http.Client{Timeout: timeout, Transport: transport}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", nil, err
		}
		req.Header.Set("User-Agent", ua)
		resp, err := client.Do(req)
		if err != nil {
			return "", nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", resp.Header, fmt.Errorf("HTTP %s", resp.Status)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxRuleListBytes+1))
		if err != nil {
			return "", resp.Header, err
		}
		if len(b) > maxRuleListBytes {
			return "", resp.Header, fmt.Errorf("response exceeds 16 MiB limit")
		}
		return string(b), resp.Header, nil
	}

	switch downloadMode {
	case "direct":
		return doFetch(http.DefaultTransport, 15*time.Second)
	case "group":
		if strings.TrimSpace(groupName) == "" {
			return "", nil, fmt.Errorf("download mode is 'group' but no download group is specified")
		}
		tr, err := dae.GroupHttpTransport(groupName)
		if err != nil {
			return "", nil, err
		}
		return doFetch(tr, 20*time.Second)
	default: // "auto": direct first, fallback to dae routing
		body, hdr, err := doFetch(http.DefaultTransport, 10*time.Second)
		if err == nil {
			return body, hdr, nil
		}
		body2, hdr2, err2 := doFetch(dae.HttpTransport, 10*time.Second)
		if err2 != nil {
			if errors.Is(err2, dae.ErrControlPlaneNotInit) {
				return "", nil, err
			}
			return "", nil, fmt.Errorf("%v (direct); %w (route)", err, err2)
		}
		return body2, hdr2, nil
	}
}

// RefreshByID downloads and validates a remote rule list outside DB transactions,
// preserving the last good version on any fetch or parse error.
func RefreshByID(ctx context.Context, id uint, fromCron bool) (*RefreshResult, error) {
	mu := lockForList(id)
	mu.Lock()
	defer mu.Unlock()

	var m db.RuleList
	if err := db.DB(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		return nil, err
	}
	if m.SourceType != "remote" {
		parsed := ParseRuleList(m.RawText, m.Kind)
		return &RefreshResult{RuleList: &m, Diagnostics: parsed.Diagnostics}, nil
	}
	origRevision := m.Revision

	// Network I/O strictly outside DB transaction.
	body, _, fetchErr := FetchRemoteContent(ctx, m.URL, m.DownloadMode, m.DownloadGroupName, m.UserAgent)
	if fetchErr != nil {
		_ = db.DB(ctx).Model(&db.RuleList{ID: id}).Updates(map[string]interface{}{
			"last_status": "error",
			"last_error":  fetchErr.Error(),
		}).Error
		return nil, fetchErr
	}
	if strings.TrimSpace(body) == "" {
		err := fmt.Errorf("empty response from rule list source")
		_ = db.DB(ctx).Model(&db.RuleList{ID: id}).Updates(map[string]interface{}{
			"last_status": "error",
			"last_error":  err.Error(),
		}).Error
		return nil, err
	}
	parsed := ParseRuleList(body, m.Kind)
	if parsed.HasErrors || len(parsed.Entries) == 0 {
		errMsg := "no valid entries found"
		if len(parsed.Diagnostics) > 0 {
			errMsg = parsed.Diagnostics[0].Message
		}
		_ = db.DB(ctx).Model(&db.RuleList{ID: id}).Updates(map[string]interface{}{
			"last_status": "error",
			"last_error":  errMsg,
		}).Error
		return &RefreshResult{
			RuleList:    &m,
			Diagnostics: parsed.Diagnostics,
		}, fmt.Errorf("rule list %q rejected (keeping last good version): %s", m.Name, errMsg)
	}

	newDigest := contentDigest(body)
	tx := db.BeginTx(ctx)
	defer tx.Rollback()

	var cur db.RuleList
	if err := tx.Where("id = ?", id).First(&cur).Error; err != nil {
		return nil, err
	}
	if cur.Revision != origRevision {
		return nil, fmt.Errorf("rule list %q was modified concurrently during refresh (revision %d != %d)", cur.Name, cur.Revision, origRevision)
	}
	changed := cur.LastDigest != newDigest
	added, removed := DiffEntryCounts(cur.LastContent, body, cur.Kind)
	now := time.Now()
	cur.LastContent = body
	cur.LastDigest = newDigest
	cur.LastEntryCount = len(parsed.Entries)
	cur.LastAddedCount = added
	cur.LastRemovedCount = removed
	cur.LastStatus = "ready"
	cur.LastError = ""
	cur.LastUpdatedAt = &now
	if changed {
		cur.Version++
	}
	if err := tx.Save(&cur).Error; err != nil {
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	if changed && fromCron {
		go triggerAutoApply(fmt.Sprintf("rulelist:%s", cur.Name))
	}
	return &RefreshResult{
		RuleList:     &cur,
		Changed:      changed,
		AddedCount:   added,
		RemovedCount: removed,
		Diagnostics:  parsed.Diagnostics,
	}, nil
}

func StartAllSchedulers(ctx context.Context) {
	var lists []db.RuleList
	if err := db.DB(ctx).Find(&lists).Error; err != nil {
		logrus.WithError(err).Warn("load rule lists for scheduler")
		return
	}
	for _, l := range lists {
		SyncScheduler(ctx, l.ID)
	}
}

func SyncScheduler(ctx context.Context, id uint) {
	RemoveScheduler(id)
	var l db.RuleList
	if err := db.DB(ctx).Where("id = ?", id).First(&l).Error; err != nil {
		return
	}
	if !l.CronEnable || l.SourceType != "remote" || strings.TrimSpace(l.CronExp) == "" {
		return
	}
	schedulerMu.Lock()
	defer schedulerMu.Unlock()
	s := gocron.NewScheduler(time.Local)
	_, err := s.Cron(l.CronExp).Do(func() {
		if _, err := RefreshByID(context.Background(), l.ID, true); err != nil {
			logrus.WithError(err).Warnf("scheduled refresh of rule list %q failed", l.Name)
		}
	})
	if err != nil {
		logrus.WithError(err).Warnf("schedule rule list %q failed", l.Name)
		s.Stop()
		return
	}
	s.StartAsync()
	schedulerCache[l.ID] = s
}

func RemoveScheduler(id uint) {
	schedulerMu.Lock()
	defer schedulerMu.Unlock()
	if s, ok := schedulerCache[id]; ok {
		s.Stop()
		delete(schedulerCache, id)
	}
}
