/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae/component/outbound/dialer"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type SaveChainInput struct {
	ID            *uint
	Name          string
	Tag           *string
	MemberNodeIDs []uint
}

type ChainInfo struct {
	Chain       db.ProxyChain
	MemberIDs   []uint
	MemberNames []string
	Parseable   bool
	ParseError  string
}

func ListAll(ctx context.Context) ([]ChainInfo, error) {
	d := db.DB(ctx)
	var chains []db.ProxyChain
	if err := d.Order("id asc").Find(&chains).Error; err != nil {
		return nil, err
	}
	var allNodes []db.Node
	_ = d.Find(&allNodes).Error
	nodeByID := make(map[uint]db.Node, len(allNodes))
	for _, n := range allNodes {
		nodeByID[n.ID] = n
	}
	out := make([]ChainInfo, 0, len(chains))
	for _, ch := range chains {
		out = append(out, buildChainInfo(ch, nodeByID))
	}
	return out, nil
}

func buildChainInfo(ch db.ProxyChain, nodeByID map[uint]db.Node) ChainInfo {
	var ids []uint
	_ = json.Unmarshal([]byte(ch.MemberNodeIDsJSON), &ids)
	names := make([]string, 0, len(ids))
	links := make([]string, 0, len(ids))
	info := ChainInfo{Chain: ch, MemberIDs: ids, Parseable: true}
	if len(ids) < 2 {
		info.Parseable = false
		info.ParseError = "proxy chain requires at least 2 leaf nodes"
		return info
	}
	for _, id := range ids {
		n, ok := nodeByID[id]
		if !ok {
			names = append(names, fmt.Sprintf("<missing:%d>", id))
			info.Parseable = false
			info.ParseError = fmt.Sprintf("leaf node %d not found", id)
			continue
		}
		names = append(names, n.Name)
		if n.NeedsRebind {
			info.Parseable = false
			info.ParseError = fmt.Sprintf("leaf node %q needs rebind: %s", n.Name, n.RebindReason)
			continue
		}
		links = append(links, n.Link)
	}
	info.MemberNames = names
	if info.Parseable && len(links) == len(ids) {
		chainLink := strings.Join(links, " -> ")
		noLog := logrus.New()
		noLog.SetOutput(io.Discard)
		dl, err := dialer.NewFromLinkContext(context.Background(), &dialer.GlobalOption{Log: noLog}, dialer.InstanceOption{DisableCheck: true}, chainLink, ch.Name)
		if err != nil {
			info.Parseable = false
			info.ParseError = err.Error()
		} else {
			_ = dl.Close()
		}
	}
	return info
}

func Save(ctx context.Context, in SaveChainInput) (*ChainInfo, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("proxy chain name is required")
	}
	if len(in.MemberNodeIDs) < 2 {
		return nil, fmt.Errorf("proxy chain must have at least 2 ordered leaf nodes")
	}
	seen := make(map[uint]bool, len(in.MemberNodeIDs))
	for _, id := range in.MemberNodeIDs {
		if seen[id] {
			return nil, fmt.Errorf("duplicate leaf node ID %d in proxy chain", id)
		}
		seen[id] = true
	}

	tx := db.BeginTx(ctx)
	defer tx.Rollback()

	var nodes []db.Node
	if err := tx.Where("id in ?", in.MemberNodeIDs).Find(&nodes).Error; err != nil {
		return nil, err
	}
	nodeByID := make(map[uint]db.Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}
	for _, id := range in.MemberNodeIDs {
		n, ok := nodeByID[id]
		if !ok {
			return nil, fmt.Errorf("leaf node ID %d not found", id)
		}
		if n.NeedsRebind {
			return nil, fmt.Errorf("leaf node %q needs rebind before joining a chain", n.Name)
		}
	}

	idsJSON, _ := json.Marshal(in.MemberNodeIDs)
	var m db.ProxyChain
	if in.ID != nil && *in.ID > 0 {
		if err := tx.Where("id = ?", *in.ID).First(&m).Error; err != nil {
			return nil, err
		}
		m.Name = name
		m.Tag = in.Tag
		m.MemberNodeIDsJSON = string(idsJSON)
		m.NeedsRebind = false
		m.RebindReason = ""
		m.Version++
		if err := tx.Save(&m).Error; err != nil {
			return nil, err
		}
	} else {
		m = db.ProxyChain{
			Name:              name,
			Tag:               in.Tag,
			MemberNodeIDsJSON: string(idsJSON),
			Version:           1,
		}
		if err := tx.Create(&m).Error; err != nil {
			return nil, err
		}
	}

	// Bump version of any groups referencing this chain.
	_ = tx.Exec(`UPDATE groups SET version = version + 1 WHERE id IN (SELECT group_id FROM group_chains WHERE proxy_chain_id = ?)`, m.ID).Error

	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	info := buildChainInfo(m, nodeByID)
	return &info, nil
}

func Delete(ctx context.Context, chainID uint) error {
	tx := db.BeginTx(ctx)
	defer tx.Rollback()

	// Check group references.
	var groupNames []string
	_ = tx.Raw(`SELECT g.name FROM groups g INNER JOIN group_chains gc ON gc.group_id = g.id WHERE gc.proxy_chain_id = ?`, chainID).Scan(&groupNames).Error
	if len(groupNames) > 0 {
		return fmt.Errorf("cannot delete proxy chain: still referenced by groups %s", strings.Join(groupNames, ", "))
	}
	// Check failover references.
	key := fmt.Sprintf("chain:%d", chainID)
	var fcs []db.GroupFailover
	if err := tx.Find(&fcs).Error; err == nil {
		for _, fc := range fcs {
			if fc.PrimaryMemberKey == key || strings.Contains(fc.BackupMemberKeysJSON, fmt.Sprintf("%q", key)) {
				return fmt.Errorf("cannot delete proxy chain: still referenced by group failover config (group %d)", fc.GroupID)
			}
		}
	}
	if err := tx.Delete(&db.ProxyChain{ID: chainID}).Error; err != nil {
		return err
	}
	return tx.Commit().Error
}

func GroupAddChains(ctx context.Context, groupID uint, chainIDs []uint) (int32, error) {
	tx := db.BeginTx(ctx)
	defer tx.Rollback()
	var g db.Group
	if err := tx.Where("id = ?", groupID).First(&g).Error; err != nil {
		return 0, err
	}
	var chains []db.ProxyChain
	if err := tx.Where("id in ?", chainIDs).Find(&chains).Error; err != nil {
		return 0, err
	}
	if err := tx.Model(&g).Association("Chains").Append(chains); err != nil {
		return 0, err
	}
	if err := tx.Model(&db.Group{ID: groupID}).Update("version", gorm.Expr("version + 1")).Error; err != nil {
		return 0, err
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return int32(len(chains)), nil
}

func GroupDelChains(ctx context.Context, groupID uint, chainIDs []uint) (int32, error) {
	tx := db.BeginTx(ctx)
	defer tx.Rollback()
	var g db.Group
	if err := tx.Where("id = ?", groupID).First(&g).Error; err != nil {
		return 0, err
	}
	var chains []db.ProxyChain
	if err := tx.Where("id in ?", chainIDs).Find(&chains).Error; err != nil {
		return 0, err
	}
	if err := tx.Model(&g).Association("Chains").Delete(chains); err != nil {
		return 0, err
	}
	if err := tx.Model(&db.Group{ID: groupID}).Update("version", gorm.Expr("version + 1")).Error; err != nil {
		return 0, err
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return int32(len(chains)), nil
}
