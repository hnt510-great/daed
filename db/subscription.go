/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2023, daeuniverse Organization <team@v2raya.org>
 */

package db

import (
	"fmt"
	"time"
)

var (
	BadLinkFormatError = fmt.Errorf("bad link format")
)

type Subscription struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	UpdatedAt  time.Time `gorm:"not null"`
	Tag        *string   `gorm:"unique"`
	Link       string    `gorm:"not null;unique"`
	Status     string    `gorm:"not null"`
	Info       string    `gorm:"not null"`
	CronExp    string    `gorm:"not null;default:''"`
	CronEnable bool      `gorm:"not null;default:false"`
	Node       []Node

	// Enhanced subscription fields.
	Format            string     `gorm:"not null;default:'auto'"`
	UserAgent         string     `gorm:"not null;default:''"`
	DownloadMode      string     `gorm:"not null;default:'auto'"` // auto | direct | group
	DownloadGroupName string     `gorm:"not null;default:''"`
	IncludeKeywords   string     `gorm:"not null;default:''"` // newline/comma separated
	ExcludeKeywords   string     `gorm:"not null;default:''"`
	FilterPriority    string     `gorm:"not null;default:'exclude_first'"` // exclude_first | include_first
	TrafficUpload     int64      `gorm:"not null;default:0"`
	TrafficDownload   int64      `gorm:"not null;default:0"`
	TrafficTotal      int64      `gorm:"not null;default:0"`
	ExpireAt          *time.Time
	LastDiffSummary   string `gorm:"not null;default:''"`
	LastError         string `gorm:"not null;default:''"`
	Revision          uint   `gorm:"not null;default:0"`
}
