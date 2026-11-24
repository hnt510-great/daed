/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package db

type ProxyChain struct {
	ID                uint    `gorm:"primaryKey;autoIncrement"`
	Name              string  `gorm:"not null;unique;index"`
	Tag               *string `gorm:"unique"`
	MemberNodeIDsJSON string  `gorm:"not null;default:'[]'"` // JSON array of ordered leaf Node IDs: [1, 2]
	NeedsRebind       bool    `gorm:"not null;default:false"`
	RebindReason      string  `gorm:"not null;default:''"`
	Version           uint    `gorm:"not null;default:1"`
}
