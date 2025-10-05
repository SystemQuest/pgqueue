package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/systemquest/pgqueue/pkg/listener"
	"github.com/systemquest/pgqueue/test/testutil"
)

// TestEventListener tests the event listener functionality at the component level
// Migrated from Python PgQueuer's test_event_listener (Commit a2fddc7)
//
// Note: This is an integration test (requires PostgreSQL), not a unit test,
// as it tests the interaction between Listener and PostgreSQL LISTEN/NOTIFY.
//
// This test validates the listener API:
// 1. Initialize event listener on a channel
// 2. Send a notification with event payload
// 3. Receive and verify the event matches what was sent
//
// Python PgQueuer tests this with both asyncpg and psycopg drivers.
// Go only uses pgx driver, so we test directly against PostgreSQL.
func TestEventListener(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create test channel name (similar to Python: f"test_event_listener_{name}")
	channel := "test_event_listener_pgx"

	// Create the event to send (similar to Python's Event model)
	sentEvent := &listener.Event{
		Channel:   channel,
		Operation: listener.OperationUpdate,
		SentAt:    time.Now().UTC(),
		Table:     "foo",
	}

	// Initialize event listener
	l := listener.NewListener(testDB.Pool(), channel, nil)
	require.NotNil(t, l)

	// Channel to receive the event
	eventReceived := make(chan *listener.Event, 1)

	// Add handler to capture the event
	l.AddHandler(listener.OperationUpdate, func(event *listener.Event) error {
		eventReceived <- event
		return nil
	})

	// Start the listener
	err := l.Start(ctx)
	require.NoError(t, err)
	defer l.Stop(ctx)

	// Wait for listener to be ready
	time.Sleep(100 * time.Millisecond)

	// Send notification (similar to Python: await notify(dd, channel, payload.model_dump_json()))
	payload, err := json.Marshal(sentEvent)
	require.NoError(t, err)

	// Use a separate connection to send the notification
	_, err = testDB.Pool().Exec(ctx, fmt.Sprintf("NOTIFY %s, '%s'", channel, string(payload)))
	require.NoError(t, err)

	// Wait for event to be received (similar to Python: await asyncio.wait_for(listener.get(), timeout=1))
	select {
	case receivedEvent := <-eventReceived:
		// Verify the event matches (similar to Python: assert ... == payload)
		assert.Equal(t, sentEvent.Channel, receivedEvent.Channel, "Channel should match")
		assert.Equal(t, sentEvent.Operation, receivedEvent.Operation, "Operation should match")
		assert.Equal(t, sentEvent.Table, receivedEvent.Table, "Table should match")

		// SentAt should be close (within 1 second due to JSON serialization precision)
		timeDiff := receivedEvent.SentAt.Sub(sentEvent.SentAt).Abs()
		assert.Less(t, timeDiff, 1*time.Second, "SentAt timestamp should be close")

		// Received timestamp should be set and after SentAt
		assert.False(t, receivedEvent.Received.IsZero(), "Received timestamp should be set")
		assert.True(t, receivedEvent.Received.After(sentEvent.SentAt), "Received should be after SentAt")

		// Latency should be reasonable (< 1 second for local test)
		latency := receivedEvent.Latency()
		assert.Greater(t, latency, time.Duration(0), "Latency should be positive")
		assert.Less(t, latency, 1*time.Second, "Latency should be reasonable for local test")

		t.Logf("✓ Event received successfully with latency: %v", latency)

	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for event")
	}
}

// TestEventListenerMultipleOperations tests that listener can handle different operation types
// This is a Go-specific enhancement to test all operation types
func TestEventListenerMultipleOperations(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	channel := "test_event_listener_multi_ops"

	// Test all operation types
	operations := []listener.Operation{
		listener.OperationInsert,
		listener.OperationUpdate,
		listener.OperationDelete,
		listener.OperationTruncate,
	}

	for _, op := range operations {
		t.Run(string(op), func(t *testing.T) {
			l := listener.NewListener(testDB.Pool(), channel, nil)
			require.NotNil(t, l)

			eventReceived := make(chan *listener.Event, 1)

			// Add handler for this specific operation
			l.AddHandler(op, func(event *listener.Event) error {
				eventReceived <- event
				return nil
			})

			err := l.Start(ctx)
			require.NoError(t, err)
			defer l.Stop(ctx)

			time.Sleep(100 * time.Millisecond)

			// Send event with this operation type
			sentEvent := &listener.Event{
				Channel:   channel,
				Operation: op,
				SentAt:    time.Now().UTC(),
				Table:     "test_table",
			}

			payload, err := json.Marshal(sentEvent)
			require.NoError(t, err)

			_, err = testDB.Pool().Exec(ctx, fmt.Sprintf("NOTIFY %s, '%s'", channel, string(payload)))
			require.NoError(t, err)

			// Verify event received
			select {
			case receivedEvent := <-eventReceived:
				assert.Equal(t, op, receivedEvent.Operation)
				t.Logf("✓ Received %s operation", op)
			case <-time.After(1 * time.Second):
				t.Fatalf("Timeout waiting for %s event", op)
			}
		})
	}
}

// TestEventListenerAddHandlerAll tests that AddHandlerAll registers handler for all operations
// This validates the convenience method for catching all events
func TestEventListenerAddHandlerAll(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	channel := "test_event_listener_all"

	l := listener.NewListener(testDB.Pool(), channel, nil)
	require.NotNil(t, l)

	eventsReceived := make(chan *listener.Event, 4)

	// Use AddHandlerAll to catch all operation types
	l.AddHandlerAll(func(event *listener.Event) error {
		eventsReceived <- event
		return nil
	})

	err := l.Start(ctx)
	require.NoError(t, err)
	defer l.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Send events with different operations
	operations := []listener.Operation{
		listener.OperationInsert,
		listener.OperationUpdate,
		listener.OperationDelete,
		listener.OperationTruncate,
	}

	for _, op := range operations {
		sentEvent := &listener.Event{
			Channel:   channel,
			Operation: op,
			SentAt:    time.Now().UTC(),
			Table:     "test_table",
		}

		payload, err := json.Marshal(sentEvent)
		require.NoError(t, err)

		_, err = testDB.Pool().Exec(ctx, fmt.Sprintf("NOTIFY %s, '%s'", channel, string(payload)))
		require.NoError(t, err)
	}

	// Verify all events received
	receivedOps := make(map[listener.Operation]bool)
	timeout := time.After(2 * time.Second)

	for i := 0; i < len(operations); i++ {
		select {
		case event := <-eventsReceived:
			receivedOps[event.Operation] = true
			t.Logf("✓ Received %s operation via AddHandlerAll", event.Operation)
		case <-timeout:
			t.Fatalf("Timeout: only received %d/%d events", len(receivedOps), len(operations))
		}
	}

	// Verify all operation types were received
	for _, op := range operations {
		assert.True(t, receivedOps[op], "Should have received %s operation", op)
	}
}

// TestEventListenerChannelIsolation tests that listeners on different channels are isolated
// This ensures events on one channel don't leak to listeners on other channels
func TestEventListenerChannelIsolation(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	channel1 := "test_channel_1"
	channel2 := "test_channel_2"

	// Create two listeners on different channels
	l1 := listener.NewListener(testDB.Pool(), channel1, nil)
	l2 := listener.NewListener(testDB.Pool(), channel2, nil)

	events1 := make(chan *listener.Event, 1)
	events2 := make(chan *listener.Event, 1)

	l1.AddHandlerAll(func(event *listener.Event) error {
		events1 <- event
		return nil
	})

	l2.AddHandlerAll(func(event *listener.Event) error {
		events2 <- event
		return nil
	})

	require.NoError(t, l1.Start(ctx))
	require.NoError(t, l2.Start(ctx))
	defer l1.Stop(ctx)
	defer l2.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Send event to channel1
	event1 := &listener.Event{
		Channel:   channel1,
		Operation: listener.OperationInsert,
		SentAt:    time.Now().UTC(),
		Table:     "table1",
	}

	payload1, err := json.Marshal(event1)
	require.NoError(t, err)

	_, err = testDB.Pool().Exec(ctx, fmt.Sprintf("NOTIFY %s, '%s'", channel1, string(payload1)))
	require.NoError(t, err)

	// Verify only listener1 receives it
	select {
	case event := <-events1:
		assert.Equal(t, channel1, event.Channel)
		t.Log("✓ Listener 1 received event on channel1")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Listener 1 should have received event")
	}

	// Verify listener2 does NOT receive it
	select {
	case <-events2:
		t.Fatal("Listener 2 should NOT have received event from channel1")
	case <-time.After(200 * time.Millisecond):
		t.Log("✓ Listener 2 correctly did not receive event from channel1")
	}

	// Send event to channel2
	event2 := &listener.Event{
		Channel:   channel2,
		Operation: listener.OperationUpdate,
		SentAt:    time.Now().UTC(),
		Table:     "table2",
	}

	payload2, err := json.Marshal(event2)
	require.NoError(t, err)

	_, err = testDB.Pool().Exec(ctx, fmt.Sprintf("NOTIFY %s, '%s'", channel2, string(payload2)))
	require.NoError(t, err)

	// Verify only listener2 receives it
	select {
	case event := <-events2:
		assert.Equal(t, channel2, event.Channel)
		t.Log("✓ Listener 2 received event on channel2")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Listener 2 should have received event")
	}

	// Verify listener1 does NOT receive it
	select {
	case <-events1:
		t.Fatal("Listener 1 should NOT have received event from channel2")
	case <-time.After(200 * time.Millisecond):
		t.Log("✓ Listener 1 correctly did not receive event from channel2")
	}
}
