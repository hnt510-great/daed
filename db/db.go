/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2023, daeuniverse Organization <team@v2raya.org>
 */

package db

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daeuniverse/dae-wing/pkg/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	filename            = "wing.db"
	currentSchemaVer    = 2
	maxSchemaBackupKeep = 5
)

var (
	db *gorm.DB
)

// backupBeforeMigration creates a WAL-safe consistent SQLite backup using
// VACUUM INTO before applying schema migrations to an existing database.
func backupBeforeMigration(d *gorm.DB, dbPath string) error {
	var userVer int
	if err := d.Raw("PRAGMA user_version").Scan(&userVer).Error; err != nil {
		return fmt.Errorf("read sqlite user_version: %w", err)
	}
	if userVer >= currentSchemaVer {
		return nil
	}
	// Check whether the database already has tables (i.e. an existing DB being upgraded).
	var tableCount int64
	if err := d.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tableCount).Error; err != nil {
		return fmt.Errorf("inspect sqlite tables: %w", err)
	}
	if tableCount == 0 {
		return nil
	}

	ts := time.Now().Format("20060102-150405")
	backupPath := fmt.Sprintf("%s.pre-v%d-%s.bak", dbPath, currentSchemaVer, ts)
	// Escape single quotes for VACUUM INTO literal.
	escaped := strings.ReplaceAll(backupPath, "'", "''")
	if err := d.Exec(fmt.Sprintf("VACUUM INTO '%s'", escaped)).Error; err != nil {
		return fmt.Errorf("consistent sqlite backup failed: %w", err)
	}
	_ = os.Chmod(backupPath, 0640)
	return nil
}

func InitDatabase(configDir string) (err error) {
	path := filepath.Join(configDir, filename)
	db, err = gorm.Open(sqlite.Open(path), &gorm.Config{
		// Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return fmt.Errorf("%w: %v", err, path)
	}
	if err = backupBeforeMigration(db, path); err != nil {
		return err
	}
	if err = db.AutoMigrate(
		&User{},
		&Config{},
		&Dns{},
		&Routing{},
		&Node{},
		&Subscription{},
		&Group{},
		&GroupSubscription{},
		&GroupPolicyParam{},
		&RuleList{},
		&ProxyChain{},
		&GroupFailover{},
		&ConfigSnapshot{},
		&System{},
	); err != nil {
		return err
	}
	if err = db.Exec(fmt.Sprintf("PRAGMA user_version = %d", currentSchemaVer)).Error; err != nil {
		return fmt.Errorf("set sqlite user_version: %w", err)
	}
	if fi, err := os.Stat(path); err != nil {
		return err
	} else if fi.Mode()&0037 > 0 {
		// Too open, chmod it to 0640.
		if err = os.Chmod(path, 0640); err != nil {
			return err
		}
	}

	return nil
}

// SetTestDB injects a gorm.DB handle for unit tests.
func SetTestDB(testDB *gorm.DB) {
	db = testDB
}

func DB(ctx context.Context) *gorm.DB {
	return db.WithContext(ctx)
}
func SetOutput(writer io.Writer) {
	db.Logger = logger.New(log.New(writer, "\r\n", log.LstdFlags), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: false,
		Colorful:                  false,
	})
}
func BeginTx(ctx context.Context) *gorm.DB {
	return DB(ctx).Begin(&sql.TxOptions{
		Isolation: sql.LevelSerializable,
		ReadOnly:  false,
	})
}
func BeginReadOnlyTx(ctx context.Context) *gorm.DB {
	return DB(ctx).Begin(&sql.TxOptions{
		Isolation: sql.LevelSerializable,
		ReadOnly:  true,
	})
}
