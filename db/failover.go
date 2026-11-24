/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package db

type GroupFailover struct {
	ID                   uint   `gorm:"primaryKey;autoIncrement"`
	GroupID              uint   `gorm:"not null;uniqueIndex"`
	Enabled              bool   `gorm:"not null;default:false"`
	PrimaryMemberKey     string `gorm:"not null;default:''"` // "node:<id>" or "chain:<id>"
	BackupMemberKeysJSON string `gorm:"not null;default:'[]'"`
	ProbeIntervalSec     int    `gorm:"not null;default:30"`
	ProbeTimeoutSec      int    `gorm:"not null;default:5"`
	FailThreshold        int    `gorm:"not null;default:3"`
	RecoverThreshold     int    `gorm:"not null;default:2"`
	AutoFailback         bool   `gorm:"not null;default:true"`
	ProbeURL             string `gorm:"not null;default:'http://www.gstatic.com/generate_204'"`
	ProbeDNS             string `gorm:"not null;default:'1.1.1.1:53'"`
	Revision             uint   `gorm:"not null;default:1"`
}
