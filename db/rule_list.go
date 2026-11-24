/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package db

import "time"

type RuleList struct {
	ID                uint       `gorm:"primaryKey;autoIncrement"`
	Name              string     `gorm:"not null;unique;index"`
	Kind              string     `gorm:"not null;default:'domain'"` // direct | proxy | block | custom_group | gfw | cn_domain | ipv4_cidr | ipv6_cidr | domain
	TargetGroup       string     `gorm:"not null;default:''"`
	SourceType        string     `gorm:"not null;default:'static'"` // static | remote
	URL               string     `gorm:"not null;default:''"`
	DownloadMode      string     `gorm:"not null;default:'auto'"` // auto | direct | group
	DownloadGroupName string     `gorm:"not null;default:''"`
	UserAgent         string     `gorm:"not null;default:''"`
	CronExp           string     `gorm:"not null;default:''"`
	CronEnable        bool       `gorm:"not null;default:false"`
	RawText           string     `gorm:"not null;default:''"`
	LastContent       string     `gorm:"not null;default:''"`
	LastDigest        string     `gorm:"not null;default:''"`
	LastEntryCount    int        `gorm:"not null;default:0"`
	LastAddedCount    int        `gorm:"not null;default:0"`
	LastRemovedCount  int        `gorm:"not null;default:0"`
	LastStatus        string     `gorm:"not null;default:'ready'"` // ready | error | pending
	LastError         string     `gorm:"not null;default:''"`
	LastUpdatedAt     *time.Time
	Revision          uint `gorm:"not null;default:1"`
	Version           uint `gorm:"not null;default:1"`
}
