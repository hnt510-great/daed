/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"time"

	"github.com/daeuniverse/dae-wing/dae"
	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae-wing/graphql/service/failover"
	"github.com/daeuniverse/dae/common/consts"
	"github.com/daeuniverse/dae/component/outbound/dialer"
	"github.com/daeuniverse/dae/control"
	"github.com/sirupsen/logrus"
)

type NodePathProbeResult struct {
	TargetKind  string                `json:"targetKind"` // node | chain
	TargetID    uint                  `json:"targetId"`
	TargetName  string                `json:"targetName"`
	ProbeType   string                `json:"probeType"` // icmp | tcp | url | udp_dns
	Destination string                `json:"destination"`
	Protocol    string                `json:"protocol"`
	Parseable   bool                  `json:"parseable"`
	OK          bool                  `json:"ok"`
	LatencyMs   int64                 `json:"latencyMs"`
	Stage       string                `json:"stage"` // parse | connect | http | dns | complete
	Message     string                `json:"message"`
	TestedAt    time.Time             `json:"testedAt"`
	HopResults  []NodePathProbeResult `json:"hopResults,omitempty"`
}

func TestNodePath(ctx context.Context, targetKind string, targetID uint, probeType string, destination string) (*NodePathProbeResult, error) {
	d := db.DB(ctx)
	pt := strings.ToLower(strings.TrimSpace(probeType))
	if pt == "" {
		pt = "url"
	}
	tk := strings.ToLower(strings.TrimSpace(targetKind))
	if tk == "" {
		tk = "node"
	}

	if tk == "chain" {
		var ch db.ProxyChain
		if err := d.Where("id = ?", targetID).First(&ch).Error; err != nil {
			return nil, err
		}
		var hopIDs []uint
		_ = json.Unmarshal([]byte(ch.MemberNodeIDsJSON), &hopIDs)
		res := &NodePathProbeResult{
			TargetKind:  "chain",
			TargetID:    ch.ID,
			TargetName:  ch.Name,
			ProbeType:   pt,
			Destination: destination,
			Protocol:    "chain",
			TestedAt:    time.Now(),
		}
		if len(hopIDs) < 2 {
			res.Stage = "parse"
			res.Message = "chain requires at least 2 leaf nodes"
			return res, nil
		}
		var links []string
		for _, hid := range hopIDs {
			var leaf db.Node
			if err := d.Where("id = ?", hid).First(&leaf).Error; err != nil {
				res.Stage = "parse"
				res.Message = fmt.Sprintf("missing hop node %d", hid)
				return res, nil
			}
			links = append(links, leaf.Link)
			// Also run individual hop probe so UI separates single-hop vs full-chain!
			hopRes, _ := probeSingleLink(ctx, "node", leaf.ID, leaf.Name, leaf.Protocol, leaf.Address, leaf.Link, pt, destination)
			if hopRes != nil {
				res.HopResults = append(res.HopResults, *hopRes)
			}
		}
		fullLink := strings.Join(links, " -> ")
		chainRes, err := probeSingleLink(ctx, "chain", ch.ID, ch.Name, "chain", "", fullLink, pt, destination)
		if err != nil {
			return nil, err
		}
		chainRes.HopResults = res.HopResults
		return chainRes, nil
	}

	var n db.Node
	if err := d.Where("id = ?", targetID).First(&n).Error; err != nil {
		return nil, err
	}
	return probeSingleLink(ctx, "node", n.ID, n.Name, n.Protocol, n.Address, n.Link, pt, destination)
}

func probeSingleLink(ctx context.Context, kind string, id uint, name, proto, addr, link, probeType, dest string) (*NodePathProbeResult, error) {
	res := &NodePathProbeResult{
		TargetKind:  kind,
		TargetID:    id,
		TargetName:  name,
		ProbeType:   probeType,
		Destination: dest,
		Protocol:    proto,
		TestedAt:    time.Now(),
	}
	noLog := logrus.New()
	noLog.SetOutput(io.Discard)
	dl, err := dialer.NewFromLinkContext(ctx, &dialer.GlobalOption{Log: noLog}, dialer.InstanceOption{DisableCheck: true}, link, name)
	if err != nil {
		res.Parseable = false
		res.Stage = "parse"
		res.Message = err.Error()
		return res, nil
	}
	defer dl.Close()
	res.Parseable = true
	if addr == "" && dl.Property() != nil {
		addr = dl.Property().Address
	}

	timeout := 5 * time.Second
	switch probeType {
	case "icmp":
		host := addr
		if h, _, err := net.SplitHostPort(addr); err == nil {
			host = h
		}
		res.Destination = host
		if host == "" {
			res.Stage = "connect"
			res.Message = "no server address for ICMP probe"
			return res, nil
		}
		start := time.Now()
		cmdCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		out, err := exec.CommandContext(cmdCtx, "ping", "-c", "1", "-W", "4", host).CombinedOutput()
		res.LatencyMs = time.Since(start).Milliseconds()
		if err != nil {
			res.Stage = "connect"
			res.Message = fmt.Sprintf("icmp failed: %v (%s)", err, strings.TrimSpace(string(out)))
			return res, nil
		}
		res.OK = true
		res.Stage = "complete"
		res.Message = "icmp reply received"
		return res, nil

	case "tcp":
		res.Destination = addr
		if addr == "" {
			res.Stage = "connect"
			res.Message = "no server address for TCP connect probe"
			return res, nil
		}
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, timeout)
		res.LatencyMs = time.Since(start).Milliseconds()
		if err != nil {
			res.Stage = "connect"
			res.Message = err.Error()
			return res, nil
		}
		_ = conn.Close()
		res.OK = true
		res.Stage = "complete"
		res.Message = "tcp handshake succeeded"
		return res, nil

	case "udp_dns":
		if strings.TrimSpace(dest) == "" {
			dest = "1.1.1.1:53"
		}
		res.Destination = dest
		ok, lat, err := failover.ProbeDialerUDPDNS(ctx, dl, dest, timeout)
		res.LatencyMs = lat.Milliseconds()
		if !ok || err != nil {
			res.Stage = "dns"
			if err != nil {
				res.Message = err.Error()
			} else {
				res.Message = "udp dns probe failed"
			}
			return res, nil
		}
		res.OK = true
		res.Stage = "complete"
		res.Message = "udp dns response verified"
		return res, nil

	default: // "url"
		if strings.TrimSpace(dest) == "" {
			dest = "http://www.gstatic.com/generate_204"
		}
		res.Destination = dest
		ok, lat, err := failover.ProbeDialerHTTP(ctx, dl, dest, timeout)
		res.LatencyMs = lat.Milliseconds()
		if !ok || err != nil {
			res.Stage = "http"
			if err != nil {
				res.Message = err.Error()
			} else {
				res.Message = "http url probe failed"
			}
			return res, nil
		}
		res.OK = true
		res.Stage = "complete"
		res.Message = "http 2xx/204 response verified"
		return res, nil
	}
}

type TraceRouteInput struct {
	Network string // tcp | udp
	Domain  *string
	DstIP   *string
	DstPort uint16
	SrcIP   *string
	SrcPort *uint16
	Pname   *string
}

type TraceRuleStep struct {
	RuleID        string   `json:"ruleId"`
	Expression    string   `json:"expression"`
	Result        string   `json:"result"`
	MissingInputs []string `json:"missingInputs"`
}

type TraceRouteResult struct {
	Available         bool            `json:"available"`
	UnavailableReason string          `json:"unavailableReason"`
	Generation        uint64          `json:"generation"`
	ActiveDigest      string          `json:"activeDigest"`
	Decision          string          `json:"decision"` // hit | miss | indeterminate | unavailable
	Outbound          string          `json:"outbound"`
	MissingInputs     []string        `json:"missingInputs"`
	Rules             []TraceRuleStep `json:"rules"`
}

func TraceActiveRoute(ctx context.Context, in TraceRouteInput) (*TraceRouteResult, error) {
	res := &TraceRouteResult{
		Generation: dae.ActiveGeneration(),
	}
	var sys db.System
	if err := db.DB(ctx).Model(&db.System{}).First(&sys).Error; err == nil {
		res.ActiveDigest = sys.ActiveDigest
	}

	ctl, err := dae.ControlPlane()
	if err != nil {
		res.Available = false
		res.Decision = "unavailable"
		res.UnavailableReason = "proxy control plane is not running"
		return res, nil
	}
	if !ctl.RoutingTraceAvailable() {
		res.Available = false
		res.Decision = "unavailable"
		res.UnavailableReason = fmt.Sprintf("routing trace is unavailable on active control plane (disabled or rules exceed %d step limit)", control.MaxRoutingTraceRuleSteps)
		return res, nil
	}
	res.Available = true

	l4 := consts.L4ProtoType_TCP
	if strings.EqualFold(strings.TrimSpace(in.Network), "udp") {
		l4 = consts.L4ProtoType_UDP
	}
	ti := control.RoutingTraceInput{
		Network: l4,
		Domain:  in.Domain,
		DstPort: in.DstPort,
		SrcPort: in.SrcPort,
		Pname:   in.Pname,
	}
	if in.DstIP != nil && strings.TrimSpace(*in.DstIP) != "" {
		addr, err := netip.ParseAddr(strings.TrimSpace(*in.DstIP))
		if err != nil {
			return nil, fmt.Errorf("invalid dstIP %q: %w", *in.DstIP, err)
		}
		ti.DstIP = &addr
	}
	if in.SrcIP != nil && strings.TrimSpace(*in.SrcIP) != "" {
		addr, err := netip.ParseAddr(strings.TrimSpace(*in.SrcIP))
		if err != nil {
			return nil, fmt.Errorf("invalid srcIP %q: %w", *in.SrcIP, err)
		}
		ti.SrcIP = &addr
	}

	eval, err := ctl.TraceRouting(ctx, ti)
	if err != nil {
		return nil, err
	}
	res.Decision = eval.Decision
	if eval.Outbound != nil {
		res.Outbound = *eval.Outbound
	}
	res.MissingInputs = eval.MissingInputs
	for _, r := range eval.Rules {
		expr := ""
		if r.Expression != nil {
			expr = *r.Expression
		}
		res.Rules = append(res.Rules, TraceRuleStep{
			RuleID:        r.RuleID,
			Expression:    expr,
			Result:        r.Result,
			MissingInputs: r.MissingInputs,
		})
	}
	return res, nil
}
