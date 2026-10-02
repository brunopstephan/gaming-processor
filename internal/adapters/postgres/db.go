package postgres

import (
	"fmt"
	"time"

	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// Open builds the GORM handle over pgx. It does not connect: connectivity is
// verified by the Fx OnStart hook (Ping). GORM's implicit per-write
// transactions are disabled; transactions are always explicit (TxManager).
func Open(cfg config.Database) (*gorm.DB, error) {
	db, err := gorm.Open(gormpg.Open(cfg.URL), &gorm.Config{
		SkipDefaultTransaction: true,
		DisableAutomaticPing:   true,
		Logger:                 logger.Discard,
		NowFunc:                func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("postgres: pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}
