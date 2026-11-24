/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package db

import "time"

type ConfigSnapshot struct {
	ID               uint      `gorm:"primaryKey;autoIncrement"`
	CreatedAt        time.Time `gorm:"not null"`
	Digest           string    `gorm:"not null;index"`
	ConfigID         uint      `gorm:"not null"`
	ConfigVersion    uint      `gorm:"not null"`
	DnsID            uint      `gorm:"not null"`
	DnsVersion       uint      `gorm:"not null"`
	RoutingID        uint      `gorm:"not null"`
	RoutingVersion   uint      `gorm:"not null"`
	GlobalText       string    `gorm:"not null"`
	DnsText          string    `gorm:"not null"`
	RoutingText      string    `gorm:"not null"`
	NodesJSON        string    `gorm:"not null"` // JSON array of SnapshotNode
	GroupsJSON       string    `gorm:"not null"` // JSON array of SnapshotGroup
	DependenciesJSON string    `gorm:"not null"` // JSON object of dependency versions
	Summary          string    `gorm:"not null;default:''"`
	Active           bool      `gorm:"not null;default:false"`
}
