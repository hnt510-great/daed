/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2023, daeuniverse Organization <team@v2raya.org>
 */

package db

type Node struct {
	ID       uint   `gorm:"primaryKey;autoIncrement"`
	Link     string `gorm:"not null"`
	Name     string `gorm:"not null"`
	Address  string `gorm:"not null"`
	Protocol string `gorm:"not null"`

	Tag *string `gorm:"unique"`

	// Stable identity & rebind tracking for subscription refreshes.
	StableKey    string `gorm:"not null;default:''"`
	IdentityKey  string `gorm:"not null;default:''"`
	NeedsRebind  bool   `gorm:"not null;default:false"`
	RebindReason string `gorm:"not null;default:''"`

	// Foreign keys.
	// Nil SubscriptionID indicates nodes belonging to no subscription.
	SubscriptionID *uint
	Subscription   *Subscription
}
