package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/systemquest/pgqueue/test/testutil"
)

// TestDatabaseFetchVal migrates pgqueuer's test_fetchval
// Tests basic scalar value fetching from database
func TestDatabaseFetchVal(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()

	// Test pgx driver fetchval equivalent - SELECT 1
	var result int
	err := testDB.Pool().QueryRow(ctx, "SELECT 1").Scan(&result)
	require.NoError(t, err)
	assert.Equal(t, 1, result, "Should fetch scalar value")
}

// TestDatabaseFetch migrates pgqueuer's test_fetch
// Tests fetching multiple rows and columns from database
func TestDatabaseFetch(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()

	// Test pgx driver fetch equivalent - returns [(1, 2)]
	rows, err := testDB.Pool().Query(ctx, "SELECT 1 as one, 2 as two")
	require.NoError(t, err)
	defer rows.Close()

	var results [][]int
	for rows.Next() {
		var one, two int
		err := rows.Scan(&one, &two)
		require.NoError(t, err)
		results = append(results, []int{one, two})
	}

	require.NoError(t, rows.Err())
	assert.Equal(t, [][]int{{1, 2}}, results, "Should fetch row as (1, 2)")
}

// TestDatabaseExecute migrates pgqueuer's test_execute
// Tests SQL statement execution
func TestDatabaseExecute(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()

	// Test pgx driver execute equivalent - returns CommandTag (string-like)
	tag, err := testDB.Pool().Exec(ctx, "SELECT 1 as one, 2 as two")
	require.NoError(t, err)
	assert.NotEmpty(t, tag.String(), "Should return command tag as string")
}

// TestDatabaseNotify migrates pgqueuer's test_notify
// Tests PostgreSQL LISTEN/NOTIFY mechanism
func TestDatabaseNotify(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()

	// Channel name and payload - similar to pgqueuer's test
	channel := "test_notify_pgx"
	payload := "hello_from_pgx"

	// Get separate connections for proper LISTEN/NOTIFY testing
	// Create fresh connections outside of pool for notification testing
	dbURL := testutil.DSN("testdb") // Use testdb directly for this test

	// Create listener connection - equivalent to pgqueuer's driver.add_listener
	listenerConn, err := pgx.Connect(ctx, dbURL)
	require.NoError(t, err)
	defer listenerConn.Close(ctx)

	// Create notifier connection (separate from listener like pgqueuer does)
	notifierConn, err := pgx.Connect(ctx, dbURL)
	require.NoError(t, err)
	defer notifierConn.Close(ctx)

	// Start listening - equivalent to pgqueuer's d.add_listener(channel, event.set_result)
	_, err = listenerConn.Exec(ctx, fmt.Sprintf("LISTEN %s", channel))
	require.NoError(t, err)

	// Send notification - equivalent to pgqueuer's notify(ad, channel, payload)
	_, err = notifierConn.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload)
	require.NoError(t, err)

	// Wait for notification - equivalent to pgqueuer's await asyncio.wait_for(event, timeout=1)
	notification, err := listenerConn.WaitForNotification(ctx)
	require.NoError(t, err)
	assert.Equal(t, channel, notification.Channel, "Should receive on correct channel")
	assert.Equal(t, payload, notification.Payload, "Should receive correct payload")
}
