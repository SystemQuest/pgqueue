package testutil

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/systemquest/pgqueue/pkg/config"
	"github.com/systemquest/pgqueue/pkg/db"
)

// DSN generates database connection string similar to pgqueuer's dsn()
func DSN(database string) string {
	host := getEnv("PGHOST", "localhost")
	user := getEnv("PGUSER", "testuser")
	password := getEnv("PGPASSWORD", "testpassword")
	port := getEnv("PGPORT", "5432")

	if database == "" {
		database = "testdb"
	}

	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s",
		user, password, host, port, database)
}

// TestDBConfig returns test database configuration
func TestDBConfig(database string) *config.DatabaseConfig {
	return &config.DatabaseConfig{
		URL:            DSN(database),
		MaxConnections: 5,
		MaxIdleTime:    5 * time.Minute,
		MaxLifetime:    1 * time.Hour,
		ConnectTimeout: 10 * time.Second,
	}
}

// TB is an interface that matches both *testing.T and *testing.B
type TB interface {
	Name() string
	Skip(...any)
	Skipf(format string, args ...any)
	Fatal(...any)
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// SetupTestDB creates a temporary test database - exactly like pgqueuer's create_test_database
func SetupTestDB(t TB) *db.DB {
	// Generate unique database name
	dbName := fmt.Sprintf("tmp_test_%s_%d",
		strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_"),
		time.Now().UnixNano())

	// Connect to postgres database
	adminConn, err := pgx.Connect(context.Background(), DSN("postgres"))
	if err != nil {
		t.Skipf("Failed to connect to admin database (PostgreSQL not available): %v", err)
		return nil
	}
	defer adminConn.Close(context.Background())

	// Create test database from testdb template - exactly like pgqueuer
	_, err = adminConn.Exec(context.Background(),
		fmt.Sprintf("CREATE DATABASE %s TEMPLATE testdb", dbName))
	if err != nil {
		t.Fatalf("Failed to create test database from template: %v", err)
	}

	// Connect to test database
	testDB, err := db.New(context.Background(), TestDBConfig(dbName))
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		testDB.Close()
		// Clean up database
		adminConn, err := pgx.Connect(context.Background(), DSN("postgres"))
		if err == nil {
			adminConn.Exec(context.Background(),
				fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", dbName))
			adminConn.Close(context.Background())
		}
	})

	return testDB
}

// getEnv returns environment variable or default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
