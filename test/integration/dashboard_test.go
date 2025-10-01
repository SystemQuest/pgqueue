package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/systemquest/pgqueue4go/pkg/queries"
	"github.com/systemquest/pgqueue4go/test/testutil"
)

// TestDashboardStatistics tests that the dashboard can fetch and display statistics
func TestDashboardStatistics(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	// Schema already exists from template testdb, no need to install

	// Insert test statistics data directly to test dashboard display
	// This approach is cleaner than creating and processing jobs
	insertStatsSQL := `
		INSERT INTO pgqueue_statistics (created, count, priority, time_in_queue, status, entrypoint)
		VALUES 
			(NOW(), 5, 1, interval '5 seconds', 'successful', 'email.send'),
			(NOW(), 3, 2, interval '10 seconds', 'successful', 'notification.push'),
			(NOW(), 2, 1, interval '3 seconds', 'exception', 'report.generate')
	`
	_, err := testDB.Pool().Exec(ctx, insertStatsSQL)
	require.NoError(t, err) // Fetch statistics
	stats, err := q.LogStatistics(ctx, 10)
	require.NoError(t, err)
	assert.NotEmpty(t, stats, "Should have statistics")

	// Verify statistics structure
	for _, stat := range stats {
		assert.NotZero(t, stat.Count, "Count should be non-zero")
		assert.NotEmpty(t, stat.Entrypoint, "Entrypoint should not be empty")
		assert.NotEmpty(t, stat.Status, "Status should not be empty")
		assert.NotZero(t, stat.Created, "Created timestamp should be set")
		t.Logf("Stat: Entrypoint=%s, Count=%d, Status=%s, TimeInQueue=%v, Priority=%d",
			stat.Entrypoint, stat.Count, stat.Status, stat.TimeInQueue, stat.Priority)
	}
}

// TestDashboardWithNoData tests dashboard behavior with no statistics
func TestDashboardWithNoData(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	// Schema already exists from template testdb, no need to install

	// Fetch statistics (should be empty)
	stats, err := q.LogStatistics(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, stats, "Should have no statistics when no jobs processed")
}
