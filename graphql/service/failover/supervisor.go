/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package failover

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/daeuniverse/dae-wing/dae"
	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae/component/outbound"
	"github.com/daeuniverse/dae/component/outbound/dialer"
	"github.com/daeuniverse/dae/control"
	"github.com/daeuniverse/outbound/netproxy"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type SaveFailoverInput struct {
	GroupID          uint
	Enabled          bool
	PrimaryMemberKey string
	BackupMemberKeys []string
	ProbeIntervalSec *int
	ProbeTimeoutSec  *int
	FailThreshold    *int
	RecoverThreshold *int
	AutoFailback     *bool
	ProbeURL         *string
	ProbeDNS         *string
	ExpectedRevision *uint
}

type ProtoRuntimeState struct {
	Protocol         string     `json:"protocol"` // tcp | udp
	ActiveMemberKey  string     `json:"activeMemberKey"`
	ActiveIndex      int        `json:"activeIndex"`
	ConsecFailures   int        `json:"consecFailures"`
	PrimaryConsecOK  int        `json:"primaryConsecOK"`
	NoHealthyBackup  bool       `json:"noHealthyBackup"`
	LastSwitchReason string     `json:"lastSwitchReason"`
	LastSwitchAt     *time.Time `json:"lastSwitchAt,omitempty"`
}

type MemberHealth struct {
	MemberKey string `json:"memberKey"`
	Index     int    `json:"index"`
	TCPAlive  bool   `json:"tcpAlive"`
	UDPAlive  bool   `json:"udpAlive"`
	TCPLatMs  int64  `json:"tcpLatMs"`
	UDPLatMs  int64  `json:"udpLatMs"`
	LastError string `json:"lastError"`
}

type GroupFailoverStatus struct {
	Config      db.GroupFailover
	GroupName   string
	Generation  uint64
	TCP         ProtoRuntimeState
	UDP         ProtoRuntimeState
	Members     []MemberHealth
	LastProbeAt *time.Time
}

type groupRuntime struct {
	groupID       uint
	groupName     string
	generation    uint64
	revision      uint
	orderedKeys   []string
	keyToIndex    map[string]int
	tcp           ProtoRuntimeState
	udp           ProtoRuntimeState
	memberHealth  map[string]*MemberHealth
	lastProbeAt   *time.Time
	lastProbeDone time.Time
}

type Supervisor struct {
	mu       sync.Mutex
	gen      uint64
	ctl      *control.ControlPlane
	runtimes map[uint]*groupRuntime
	stopCh   chan struct{}
	started  bool
}

var defaultSupervisor = &Supervisor{
	runtimes: make(map[uint]*groupRuntime),
}

func Default() *Supervisor {
	return defaultSupervisor
}

func (s *Supervisor) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	dae.RegisterGenerationHook(func(gen uint64, c *control.ControlPlane) {
		s.OnGenerationChanged(gen, c)
	})

	go s.loop()
}

func (s *Supervisor) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	close(s.stopCh)
	s.started = false
	s.mu.Unlock()
}

// OnGenerationChanged rebuilds memberKey -> runtimeIndex mappings from the
// active ConfigSnapshot and live ControlPlane.OutboundGroups().
func (s *Supervisor) OnGenerationChanged(gen uint64, c *control.ControlPlane) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen = gen
	s.ctl = c
	s.runtimes = make(map[uint]*groupRuntime)
	if c == nil {
		return
	}

	d := db.DB(context.Background())
	if d == nil {
		return
	}
	var snap db.ConfigSnapshot
	if err := d.Where("active = ?", true).Order("id desc").First(&snap).Error; err != nil {
		return
	}
	var snapGroups []struct {
		GroupID     uint     `json:"groupId"`
		Name        string   `json:"name"`
		MemberKeys  []string `json:"memberKeys"`
		MemberNames []string `json:"memberNames"`
		Failover    bool     `json:"failover"`
	}
	if err := json.Unmarshal([]byte(snap.GroupsJSON), &snapGroups); err != nil {
		return
	}

	var fcs []db.GroupFailover
	_ = d.Where("enabled = ?", true).Find(&fcs).Error
	fcByGroup := make(map[uint]db.GroupFailover, len(fcs))
	for _, fc := range fcs {
		fcByGroup[fc.GroupID] = fc
	}

	liveByName := make(map[string]*outbound.DialerGroup)
	for _, dg := range c.OutboundGroups() {
		if dg != nil {
			liveByName[dg.Name] = dg
		}
	}

	for _, sg := range snapGroups {
		fc, ok := fcByGroup[sg.GroupID]
		if !ok || !sg.Failover {
			continue
		}
		dg, ok := liveByName[sg.Name]
		if !ok || len(dg.Dialers) == 0 {
			continue
		}
		keyToIdx := make(map[string]int, len(sg.MemberKeys))
		memberHealth := make(map[string]*MemberHealth, len(sg.MemberKeys))
		// Match SnapshotGroup MemberNames to live DialerGroup.Dialers indices.
		for i, mk := range sg.MemberKeys {
			idx := i
			if i < len(sg.MemberNames) {
				for di, dl := range dg.Dialers {
					if dl != nil && dl.Property() != nil && dl.Property().Name == sg.MemberNames[i] {
						idx = di
						break
					}
				}
			}
			if idx < len(dg.Dialers) {
				keyToIdx[mk] = idx
				memberHealth[mk] = &MemberHealth{
					MemberKey: mk,
					Index:     idx,
					TCPAlive:  true,
					UDPAlive:  true,
				}
			}
		}
		if len(keyToIdx) == 0 {
			continue
		}
		primaryKey := sg.MemberKeys[0]
		primaryIdx := keyToIdx[primaryKey]
		s.runtimes[sg.GroupID] = &groupRuntime{
			groupID:      sg.GroupID,
			groupName:    sg.Name,
			generation:   gen,
			revision:     fc.Revision,
			orderedKeys:  sg.MemberKeys,
			keyToIndex:   keyToIdx,
			memberHealth: memberHealth,
			tcp: ProtoRuntimeState{
				Protocol:        "tcp",
				ActiveMemberKey: primaryKey,
				ActiveIndex:     primaryIdx,
			},
			udp: ProtoRuntimeState{
				Protocol:        "udp",
				ActiveMemberKey: primaryKey,
				ActiveIndex:     primaryIdx,
			},
		}
	}
}

func (s *Supervisor) loop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.probeDueGroups()
		}
	}
}

func (s *Supervisor) probeDueGroups() {
	d := db.DB(context.Background())
	if d == nil {
		return
	}
	var fcs []db.GroupFailover
	if err := d.Where("enabled = ?", true).Find(&fcs).Error; err != nil {
		return
	}
	now := time.Now()
	for _, fc := range fcs {
		interval := fc.ProbeIntervalSec
		if interval <= 0 {
			interval = 30
		}
		s.mu.Lock()
		rt, ok := s.runtimes[fc.GroupID]
		due := ok && now.Sub(rt.lastProbeDone) >= time.Duration(interval)*time.Second
		s.mu.Unlock()
		if due {
			_ = s.ProbeGroupOnce(context.Background(), fc.GroupID)
		}
	}
}

// ProbeGroupOnce runs one TCP + UDP probe cycle for all ordered members of a
// failover group, then applies failover/failback decisions serially while
// rejecting stale results if generation or failover revision changed mid-probe.
func (s *Supervisor) ProbeGroupOnce(ctx context.Context, groupID uint) error {
	d := db.DB(ctx)
	if d == nil {
		return fmt.Errorf("database not initialized")
	}
	var fc db.GroupFailover
	if err := d.Where("group_id = ?", groupID).First(&fc).Error; err != nil {
		return err
	}
	if !fc.Enabled {
		return nil
	}

	s.mu.Lock()
	rt, ok := s.runtimes[groupID]
	ctl := s.ctl
	gen := s.gen
	if !ok || ctl == nil {
		s.mu.Unlock()
		return nil
	}
	groupName := rt.groupName
	rev := rt.revision
	ordered := append([]string{}, rt.orderedKeys...)
	keyToIdx := make(map[string]int, len(rt.keyToIndex))
	for k, v := range rt.keyToIndex {
		keyToIdx[k] = v
	}
	s.mu.Unlock()

	var dg *outbound.DialerGroup
	for _, g := range ctl.OutboundGroups() {
		if g != nil && g.Name == groupName {
			dg = g
			break
		}
	}
	if dg == nil {
		return fmt.Errorf("live outbound group %q not found", groupName)
	}

	timeout := time.Duration(fc.ProbeTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	probeURL := strings.TrimSpace(fc.ProbeURL)
	if probeURL == "" {
		probeURL = "http://www.gstatic.com/generate_204"
	}
	probeDNS := strings.TrimSpace(fc.ProbeDNS)
	if probeDNS == "" {
		probeDNS = "1.1.1.1:53"
	}

	// Probe outside the lock so slow network probes never block runtime state reads.
	results := make(map[string]MemberHealth, len(ordered))
	for _, mk := range ordered {
		idx, exists := keyToIdx[mk]
		if !exists || idx < 0 || idx >= len(dg.Dialers) {
			continue
		}
		dl := dg.Dialers[idx]
		mh := MemberHealth{MemberKey: mk, Index: idx}
		tcpOK, tcpLat, tcpErr := ProbeDialerHTTP(ctx, dl, probeURL, timeout)
		mh.TCPAlive = tcpOK
		mh.TCPLatMs = tcpLat.Milliseconds()
		udpOK, udpLat, udpErr := ProbeDialerUDPDNS(ctx, dl, probeDNS, timeout)
		mh.UDPAlive = udpOK
		mh.UDPLatMs = udpLat.Milliseconds()
		if !tcpOK && tcpErr != nil {
			mh.LastError = "tcp: " + tcpErr.Error()
		} else if !udpOK && udpErr != nil {
			mh.LastError = "udp: " + udpErr.Error()
		}
		results[mk] = mh
	}

	return s.ApplyProbeResults(groupID, gen, rev, &fc, dg, results)
}

// ApplyProbeResults evaluates failover/failback state transitions for TCP and
// UDP independently, calling SetSelectionBoth when both switch to the same
// target or SetSelection per transport otherwise. Rejects stale (gen, rev).
func (s *Supervisor) ApplyProbeResults(
	groupID uint,
	probeGen uint64,
	probeRev uint,
	fc *db.GroupFailover,
	dg *outbound.DialerGroup,
	results map[string]MemberHealth,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.gen != probeGen {
		return nil // Stale probe from previous generation; discard.
	}
	rt, ok := s.runtimes[groupID]
	if !ok || rt.revision != probeRev {
		return nil // Stale probe from previous failover revision; discard.
	}

	now := time.Now()
	rt.lastProbeAt = &now
	rt.lastProbeDone = now
	for mk, mh := range results {
		cp := mh
		rt.memberHealth[mk] = &cp
	}

	failThresh := fc.FailThreshold
	if failThresh <= 0 {
		failThresh = 3
	}
	recThresh := fc.RecoverThreshold
	if recThresh <= 0 {
		recThresh = 2
	}

	nextTCPKey, nextTCPIdx, tcpChanged, tcpReason := evalProtocolTransition(
		&rt.tcp, rt.orderedKeys, rt.keyToIndex, results, true, failThresh, recThresh, fc.AutoFailback,
	)
	nextUDPKey, nextUDPIdx, udpChanged, udpReason := evalProtocolTransition(
		&rt.udp, rt.orderedKeys, rt.keyToIndex, results, false, failThresh, recThresh, fc.AutoFailback,
	)

	if dg != nil {
		if tcpChanged && udpChanged && nextTCPIdx == nextUDPIdx {
			if _, err := dg.SetSelectionBoth(nextTCPIdx); err != nil {
				logrus.WithError(err).Warnf("[Failover] group %q SetSelectionBoth(%d) failed", rt.groupName, nextTCPIdx)
				return err
			}
			rt.tcp.ActiveMemberKey = nextTCPKey
			rt.tcp.ActiveIndex = nextTCPIdx
			rt.tcp.ConsecFailures = 0
			rt.tcp.LastSwitchReason = tcpReason
			rt.tcp.LastSwitchAt = &now
			rt.udp.ActiveMemberKey = nextUDPKey
			rt.udp.ActiveIndex = nextUDPIdx
			rt.udp.ConsecFailures = 0
			rt.udp.LastSwitchReason = udpReason
			rt.udp.LastSwitchAt = &now
		} else {
			if tcpChanged {
				if _, err := dg.SetSelection(outbound.SelectionNetworkTCP, nextTCPIdx); err != nil {
					logrus.WithError(err).Warnf("[Failover] group %q SetSelection(tcp, %d) failed", rt.groupName, nextTCPIdx)
					return err
				}
				rt.tcp.ActiveMemberKey = nextTCPKey
				rt.tcp.ActiveIndex = nextTCPIdx
				rt.tcp.ConsecFailures = 0
				rt.tcp.LastSwitchReason = tcpReason
				rt.tcp.LastSwitchAt = &now
			}
			if udpChanged {
				if _, err := dg.SetSelection(outbound.SelectionNetworkUDP, nextUDPIdx); err != nil {
					logrus.WithError(err).Warnf("[Failover] group %q SetSelection(udp, %d) failed", rt.groupName, nextUDPIdx)
					return err
				}
				rt.udp.ActiveMemberKey = nextUDPKey
				rt.udp.ActiveIndex = nextUDPIdx
				rt.udp.ConsecFailures = 0
				rt.udp.LastSwitchReason = udpReason
				rt.udp.LastSwitchAt = &now
			}
		}
	}
	return nil
}

func evalProtocolTransition(
	st *ProtoRuntimeState,
	orderedKeys []string,
	keyToIdx map[string]int,
	results map[string]MemberHealth,
	isTCP bool,
	failThresh int,
	recThresh int,
	autoFailback bool,
) (targetKey string, targetIdx int, changed bool, reason string) {
	if len(orderedKeys) == 0 {
		return st.ActiveMemberKey, st.ActiveIndex, false, ""
	}
	isAlive := func(mk string) bool {
		mh, ok := results[mk]
		if !ok {
			return false
		}
		if isTCP {
			return mh.TCPAlive
		}
		return mh.UDPAlive
	}

	primaryKey := orderedKeys[0]
	// Track primary recovery count whenever not on primary.
	if st.ActiveMemberKey != primaryKey {
		if isAlive(primaryKey) {
			st.PrimaryConsecOK++
			if autoFailback && st.PrimaryConsecOK >= recThresh {
				st.PrimaryConsecOK = 0
				st.NoHealthyBackup = false
				return primaryKey, keyToIdx[primaryKey], true, fmt.Sprintf("primary %s recovered (%d consecutive successes)", primaryKey, recThresh)
			}
		} else {
			st.PrimaryConsecOK = 0
		}
	} else {
		st.PrimaryConsecOK = 0
	}

	if isAlive(st.ActiveMemberKey) {
		st.ConsecFailures = 0
		st.NoHealthyBackup = false
		return st.ActiveMemberKey, st.ActiveIndex, false, ""
	}

	st.ConsecFailures++
	if st.ConsecFailures < failThresh {
		return st.ActiveMemberKey, st.ActiveIndex, false, ""
	}

	// Threshold reached: find first healthy backup in user-configured order.
	for _, mk := range orderedKeys {
		if mk == st.ActiveMemberKey {
			continue
		}
		if isAlive(mk) {
			st.NoHealthyBackup = false
			return mk, keyToIdx[mk], true, fmt.Sprintf("failover from %s after %d consecutive failures", st.ActiveMemberKey, st.ConsecFailures)
		}
	}

	// All candidates failed: keep last selection and mark NoHealthyBackup.
	st.NoHealthyBackup = true
	return st.ActiveMemberKey, st.ActiveIndex, false, ""
}

// ProbeDialerHTTP performs a bounded HTTP GET over a dialer and expects 2xx/204.
func ProbeDialerHTTP(ctx context.Context, dl *dialer.Dialer, rawURL string, timeout time.Duration) (bool, time.Duration, error) {
	if dl == nil {
		return false, 0, fmt.Errorf("nil dialer")
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return probeHTTPViaDialer(probeCtx, dl, rawURL, timeout)
}

func probeHTTPViaDialer(ctx context.Context, dl *dialer.Dialer, rawURL string, timeout time.Duration) (bool, time.Duration, error) {
	start := time.Now()
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(c context.Context, network, addr string) (net.Conn, error) {
				conn, err := dl.DialContext(c, "tcp", addr)
				if err != nil {
					return nil, err
				}
				return &netproxy.FakeNetConn{Conn: conn}, nil
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, time.Since(start), err
	}
	resp, err := client.Do(req)
	lat := time.Since(start)
	if err != nil {
		return false, lat, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, lat, nil
	}
	return false, lat, fmt.Errorf("unexpected HTTP status %d", resp.StatusCode)
}

// ProbeDialerUDPDNS sends a real DNS A query over UDP through the dialer and
// checks for a valid DNS response header with matching transaction ID.
func ProbeDialerUDPDNS(ctx context.Context, dl *dialer.Dialer, dnsAddr string, timeout time.Duration) (bool, time.Duration, error) {
	if dl == nil {
		return false, 0, fmt.Errorf("nil dialer")
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	conn, err := dl.DialContext(probeCtx, "udp", dnsAddr)
	if err != nil {
		return false, time.Since(start), err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	// Minimal DNS A query for "example.com" with txid 0x42, 0x24
	query := []byte{
		0x42, 0x24, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0,
		0x00, 0x01, 0x00, 0x01,
	}
	if _, err := conn.Write(query); err != nil {
		return false, time.Since(start), err
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	lat := time.Since(start)
	if err != nil {
		return false, lat, err
	}
	if n < 12 || buf[0] != 0x42 || buf[1] != 0x24 || (buf[2]&0x80) == 0 {
		return false, lat, fmt.Errorf("invalid DNS response (%d bytes)", n)
	}
	return true, lat, nil
}

// SaveFailoverConfig persists failover settings for a group and bumps group version.
func SaveFailoverConfig(ctx context.Context, in SaveFailoverInput) (*db.GroupFailover, error) {
	tx := db.BeginTx(ctx)
	defer tx.Rollback()

	var g db.Group
	if err := tx.Where("id = ?", in.GroupID).First(&g).Error; err != nil {
		return nil, err
	}

	var fc db.GroupFailover
	err := tx.Where("group_id = ?", in.GroupID).First(&fc).Error
	isNew := err != nil
	if !isNew && in.ExpectedRevision != nil && fc.Revision != *in.ExpectedRevision {
		return nil, fmt.Errorf("failover revision conflict: current %d, expected %d", fc.Revision, *in.ExpectedRevision)
	}

	backupsJSON, _ := json.Marshal(in.BackupMemberKeys)
	fc.GroupID = in.GroupID
	fc.Enabled = in.Enabled
	fc.PrimaryMemberKey = strings.TrimSpace(in.PrimaryMemberKey)
	fc.BackupMemberKeysJSON = string(backupsJSON)
	if in.ProbeIntervalSec != nil && *in.ProbeIntervalSec > 0 {
		fc.ProbeIntervalSec = *in.ProbeIntervalSec
	} else if fc.ProbeIntervalSec <= 0 {
		fc.ProbeIntervalSec = 30
	}
	if in.ProbeTimeoutSec != nil && *in.ProbeTimeoutSec > 0 {
		fc.ProbeTimeoutSec = *in.ProbeTimeoutSec
	} else if fc.ProbeTimeoutSec <= 0 {
		fc.ProbeTimeoutSec = 5
	}
	if in.FailThreshold != nil && *in.FailThreshold > 0 {
		fc.FailThreshold = *in.FailThreshold
	} else if fc.FailThreshold <= 0 {
		fc.FailThreshold = 3
	}
	if in.RecoverThreshold != nil && *in.RecoverThreshold > 0 {
		fc.RecoverThreshold = *in.RecoverThreshold
	} else if fc.RecoverThreshold <= 0 {
		fc.RecoverThreshold = 2
	}
	if in.AutoFailback != nil {
		fc.AutoFailback = *in.AutoFailback
	} else if isNew {
		fc.AutoFailback = true
	}
	if in.ProbeURL != nil && strings.TrimSpace(*in.ProbeURL) != "" {
		fc.ProbeURL = strings.TrimSpace(*in.ProbeURL)
	} else if fc.ProbeURL == "" {
		fc.ProbeURL = "http://www.gstatic.com/generate_204"
	}
	if in.ProbeDNS != nil && strings.TrimSpace(*in.ProbeDNS) != "" {
		fc.ProbeDNS = strings.TrimSpace(*in.ProbeDNS)
	} else if fc.ProbeDNS == "" {
		fc.ProbeDNS = "1.1.1.1:53"
	}
	fc.Revision++

	if isNew {
		if err := tx.Create(&fc).Error; err != nil {
			return nil, err
		}
	} else {
		if err := tx.Save(&fc).Error; err != nil {
			return nil, err
		}
	}
	if err := tx.Model(&db.Group{ID: in.GroupID}).Update("version", gorm.Expr("version + 1")).Error; err != nil {
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return &fc, nil
}

func (s *Supervisor) GetStatus(ctx context.Context, groupID uint) (*GroupFailoverStatus, error) {
	d := db.DB(ctx)
	var g db.Group
	if err := d.Where("id = ?", groupID).First(&g).Error; err != nil {
		return nil, err
	}
	var fc db.GroupFailover
	if err := d.Where("group_id = ?", groupID).First(&fc).Error; err != nil {
		fc = db.GroupFailover{
			GroupID:          groupID,
			Enabled:          false,
			ProbeIntervalSec: 30,
			ProbeTimeoutSec:  5,
			FailThreshold:    3,
			RecoverThreshold: 2,
			AutoFailback:     true,
			ProbeURL:         "http://www.gstatic.com/generate_204",
			ProbeDNS:         "1.1.1.1:53",
		}
	}
	st := &GroupFailoverStatus{
		Config:    fc,
		GroupName: g.Name,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rt, ok := s.runtimes[groupID]; ok {
		st.Generation = rt.generation
		st.TCP = rt.tcp
		st.UDP = rt.udp
		st.LastProbeAt = rt.lastProbeAt
		for _, mk := range rt.orderedKeys {
			if mh, ok := rt.memberHealth[mk]; ok {
				st.Members = append(st.Members, *mh)
			}
		}
	}
	return st, nil
}
