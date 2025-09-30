package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/systemquest/pgqueue4go/pkg/config"
	"github.com/systemquest/pgqueue4go/pkg/queries"
)

// DB wraps the database connection pool and provides high-level operations
type DB struct {
	pool    *pgxpool.Pool
	queries *queries.Queries
}

// New creates a new database connection
func New(ctx context.Context, cfg *config.DatabaseConfig) (*DB, error) {
	// Build connection pool config
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Configure connection pool
	poolConfig.MaxConns = int32(cfg.MaxConnections)
	poolConfig.MaxConnIdleTime = cfg.MaxIdleTime
	poolConfig.MaxConnLifetime = cfg.MaxLifetime
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	// Create connection pool
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Database connection established",
		"max_connections", cfg.MaxConnections,
		"database_url", maskDatabaseURL(cfg.URL))

	db := &DB{
		pool:    pool,
		queries: queries.NewQueries(pool),
	}

	return db, nil
}

// Close closes the database connection pool
func (db *DB) Close() {
	if db.pool != nil {
		db.pool.Close()
		slog.Info("Database connection closed")
	}
}

// Queries returns the queries interface
func (db *DB) Queries() *queries.Queries {
	return db.queries
}

// Pool returns the underlying connection pool
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Health checks the database connection health
func (db *DB) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return db.pool.Ping(ctx)
}

// maskDatabaseURL masks sensitive information in database URL for logging
func maskDatabaseURL(url string) string {
	// Simple masking - in production, use a proper URL parser
	if len(url) > 20 {
		return url[:8] + "***" + url[len(url)-10:]
	}
	return "***"
}
