/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2023, daeuniverse Organization <team@v2raya.org>
 */

package config

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/daeuniverse/dae-wing/common"
	"github.com/daeuniverse/dae-wing/dae"
	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae-wing/graphql/service/config/global"
	daeConfig "github.com/daeuniverse/dae/config"
	"github.com/graph-gophers/graphql-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DefaultLanInterfaceEnv names the comma-separated LAN interfaces that a new
// config gets when it is created without any. The dashboard fills
// lan_interface from interfaces that carry a default route; on a router that
// would be the WAN, and with netlink v1.3.1 (default routes report Dst
// 0.0.0.0/0, not nil) it is always empty, so dae never hooks the LAN.
const DefaultLanInterfaceEnv = "DAED_DEFAULT_LAN_INTERFACE"

// applyDefaultLanInterface fills an empty lan_interface of a config that is
// being created. Updates keep whatever the user set, including an empty list.
func applyDefaultLanInterface(glob *global.Input) {
	if glob.LanInterface != nil && len(*glob.LanInterface) > 0 {
		return
	}
	ifs := strings.FieldsFunc(os.Getenv(DefaultLanInterfaceEnv), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	if len(ifs) > 0 {
		glob.LanInterface = &ifs
	}
}

func Create(ctx context.Context, name string, glob *global.Input) (*Resolver, error) {
	if glob == nil {
		glob = &global.Input{}
	}
	applyDefaultLanInterface(glob)
	strGlobal, err := glob.Marshal()
	if err != nil {
		return nil, err
	}
	m := db.Config{
		ID:       0,
		Name:     name,
		Global:   strGlobal,
		Selected: false,
	}
	// Check grammar and to dae config.
	c, err := dae.ParseConfig(&m.Global, nil, nil)
	if err != nil {
		return nil, err
	}
	if err = db.DB(ctx).Create(&m).Error; err != nil {
		return nil, err
	}
	return &Resolver{
		DaeGlobal: &c.Global,
		Model:     &m,
	}, nil
}

func Update(ctx context.Context, _id graphql.ID, inputGlobal global.Input) (r *Resolver, err error) {
	id, err := common.DecodeCursor(_id)
	if err != nil {
		return nil, err
	}
	tx := db.BeginTx(ctx)
	defer func() {
		if err == nil {
			err = tx.Commit().Error
		} else {
			tx.Rollback()
		}
	}()
	var m db.Config
	if err = tx.Model(&db.Config{}).Where("id = ?", id).First(&m).Error; err != nil {
		return nil, err
	}
	// Prepare to partially update.
	// Convert global string in database to daeConfig.Global.
	c, err := dae.ParseConfig(&m.Global, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("bad current config: %w", err)
	}
	// Assign input items to daeConfig.Global.
	inputGlobal.Assign(&c.Global)
	// Marshal back to string.
	marshaller := daeConfig.Marshaller{
		IndentSpace: 2,
		// We do not ignore any zero value because all values are valid.
		// That is to say, `ParseConfig` filled in all the values.
		IgnoreZero: false,
	}
	if err = marshaller.MarshalSection("global", reflect.ValueOf(c.Global), 0); err != nil {
		return nil, err
	}
	// Update.
	if err = tx.Model(&db.Config{ID: id}).Updates(map[string]interface{}{
		"global":  string(marshaller.Bytes()),
		"version": gorm.Expr("version + 1"),
	}).Error; err != nil {
		return nil, err
	}
	return &Resolver{
		DaeGlobal: &c.Global,
		Model:     &m,
	}, nil
}

func Remove(ctx context.Context, _id graphql.ID) (n int32, err error) {
	id, err := common.DecodeCursor(_id)
	if err != nil {
		return 0, err
	}
	tx := db.BeginTx(ctx)
	defer func() {
		if err == nil {
			err = tx.Commit().Error
		} else {
			tx.Rollback()
		}
	}()
	m := db.Config{ID: id}
	q := tx.Clauses(clause.Returning{Columns: []clause.Column{{Name: "selected"}}}).
		Select(clause.Associations).
		Delete(&m)
	if q.Error != nil {
		return 0, q.Error
	}
	return int32(q.RowsAffected), nil
}

type node struct {
	dbNode     *db.Node
	groups     []*db.Group
	uniqueName string
}

func matchedNodesForGroupSubscription(binding *db.GroupSubscription) ([]db.Node, error) {
	nodes := binding.Subscription.Node
	if binding.NameFilterRegex == nil || *binding.NameFilterRegex == "" {
		return nodes, nil
	}

	re, err := regexp.Compile(*binding.NameFilterRegex)
	if err != nil {
		return nil, err
	}

	var matched []db.Node
	for _, n := range nodes {
		if re.MatchString(n.Name) {
			matched = append(matched, n)
		}
	}
	return matched, nil
}

func deduplicateNodes(nodes []*node) []*node {
	set := make(map[string]*node)
	for _, node := range nodes {
		if oldNode, ok := set[node.dbNode.Link]; ok {
			oldNode.groups = append(oldNode.groups, node.groups...)
		} else {
			set[node.dbNode.Link] = node
		}
	}
	ret := make([]*node, 0, len(set))
	for _, node := range set {
		ret = append(ret, node)
	}
	return ret
}

// normNodeName normalize the name to satify the "key" format in dae config.
func normNodeName(_name string) string {
	name := []rune(_name)
	ret := make([]rune, 0, len(name))
	for _, r := range name {
		if r == ':' || r == '\'' {
			r = '_'
		}
		ret = append(ret, r)
	}
	return string(ret)
}

func uniquefyNodesName(nodes []*node) {
	// Uniquefy names of nodes.
	// Sort nodes by "has node.Tag" because node.Tag is unique but names of others may be the same with them.
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].dbNode.Tag != nil && nodes[j].dbNode.Tag == nil
	})
	nameToNodes := make(map[string]*node)
	for i := range nodes {
		node := nodes[i]
		if node.dbNode.Tag != nil {
			nameToNodes[*node.dbNode.Tag] = node
		} else {
			baseName := normNodeName(node.dbNode.Name)
			if node.dbNode.SubscriptionID != nil {
				baseName = fmt.Sprintf("%v.%v", *node.dbNode.SubscriptionID, baseName)
			}
			// SubID.Name
			wantedName := baseName
			for j := 0; ; j++ {
				_, exist := nameToNodes[wantedName]
				if !exist {
					nameToNodes[wantedName] = node
					break
				}
				// SubID.Name.1
				wantedName = fmt.Sprintf("%v.%v", baseName, j)
			}
		}
	}
	for name, node := range nameToNodes {
		node.uniqueName = name
	}
}

func Select(ctx context.Context, _id graphql.ID) (n int32, err error) {
	id, err := common.DecodeCursor(_id)
	if err != nil {
		return 0, err
	}
	tx := db.BeginTx(ctx)
	defer func() {
		if err == nil {
			err = tx.Commit().Error
		} else {
			tx.Rollback()
		}
	}()
	// Unset all selected.
	q := tx.Model(&db.Config{}).Where("selected = ?", true).Update("selected", false)
	if err = q.Error; err != nil {
		return 0, err
	}
	// Set selected.
	q = tx.Model(&db.Config{ID: id}).Update("selected", true)
	if err = q.Error; err != nil {
		return 0, err
	}
	if q.RowsAffected == 0 {
		return 0, fmt.Errorf("no such config")
	}
	return 1, nil
}

func Rename(ctx context.Context, _id graphql.ID, name string) (n int32, err error) {
	id, err := common.DecodeCursor(_id)
	if err != nil {
		return 0, err
	}
	q := db.DB(ctx).Model(&db.Config{ID: id}).
		Update("name", name)
	if q.Error != nil {
		return 0, q.Error
	}
	return int32(q.RowsAffected), nil
}

var runLock sync.Mutex

func Run(d *gorm.DB, noLoad bool) (n int32, err error) {
	if ok := runLock.TryLock(); !ok {
		return 0, fmt.Errorf("the last request didn't complete; make a cup of tea and take a break")
	}
	defer runLock.Unlock()
	//// Dry run (stop proxy).
	if noLoad {
		ch := make(chan error)
		dae.ChReloadConfigs <- &dae.ReloadMessage{
			Config:   dae.EmptyConfig,
			Callback: ch,
		}
		err = <-ch
		if err != nil {
			return 0, fmt.Errorf("failed to dryrun: %w; see more in log and report bugs", err)
		}

		// Running -> false
		var sys db.System
		if err = d.Model(&db.System{}).FirstOrCreate(&sys).Error; err != nil {
			return 0, err
		}
		if err = d.Model(&sys).Updates(map[string]interface{}{
			"running":           false,
			"last_apply_status": "stopped",
			"last_apply_error":  "",
		}).Error; err != nil {
			return 0, err
		}
		return 1, nil
	}

	//// Run selected global+dns+routing via deterministic candidate assembly & snapshot commit.
	if _, err = applyLocked(d, nil, nil, AssembleOptions{}); err != nil {
		return 0, err
	}
	return 1, nil
}
