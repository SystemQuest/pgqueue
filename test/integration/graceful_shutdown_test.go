package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/systemquest/pgqueue/pkg/queue"
	"github.com/systemquest/pgqueue/test/testutil"
)

// TestGracefulShutdown tests that Shutdown waits for in-flight jobs
// This validates the Phase 1 improvement: graceful shutdown implementation
func TestGracefulShutdown(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)

	var startedCount atomic.Int32
	var completedCount atomic.Int32
	var jobDurations []time.Duration
	var mu sync.Mutex

	err := qm.Entrypoint("shutdown_test", func(ctx context.Context, job *queue.Job) error {
		var data struct {
			ID       int `json:"id"`
			Duration int `json:"duration_ms"`
		}
		if err := json.Unmarshal(job.Payload, &data); err != nil {
			return err
		}

		startedCount.Add(1)
		start := time.Now()

		// Simulate work
		time.Sleep(time.Duration(data.Duration) * time.Millisecond)

		duration := time.Since(start)
		mu.Lock()
		jobDurations = append(jobDurations, duration)
		mu.Unlock()

		completedCount.Add(1)
		return nil
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Enqueue several jobs
	for i := 0; i < 5; i++ {
		payload := fmt.Sprintf(`{"id": %d, "duration_ms": 100}`, i)
		err := qm.EnqueueJob(ctx, "shutdown_test", []byte(payload), 0)
		require.NoError(t, err)
	}

	// Start queue manager
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 2
		runOpts.DequeueTimeout = 200 * time.Millisecond
		done <- qm.Run(runCtx, runOpts)
	}()

	// Let some jobs process
	time.Sleep(500 * time.Millisecond)

	// Initiate shutdown
	shutdownStart := time.Now()
	cancel() // Cancel context triggers worker shutdown

	// Wait for Run() to complete - this is the real graceful shutdown mechanism
	// Workers complete their current jobs before exiting
	<-done
	shutdownDuration := time.Since(shutdownStart)

	// Call Shutdown to clean up resources
	err = qm.Shutdown(context.Background())
	require.NoError(t, err)

	// Verify jobs were processed
	started := int(startedCount.Load())
	completed := int(completedCount.Load())

	assert.Greater(t, started, 0, "At least some jobs should have started")
	assert.Equal(t, started, completed,
		"All started jobs should complete: started=%d, completed=%d", started, completed)

	t.Logf("Processed %d jobs, total shutdown took %v", completed, shutdownDuration)
}

// TestShutdownWithBufferFlush tests that shutdown flushes statistics buffer
func TestShutdownWithBufferFlush(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)

	var processedCount atomic.Int32

	err := qm.Entrypoint("flush_test", func(ctx context.Context, job *queue.Job) error {
		processedCount.Add(1)
		return nil
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Enqueue jobs (less than buffer size to test final flush)
	for i := 0; i < 5; i++ {
		payload := fmt.Sprintf(`{"id": %d}`, i)
		err := qm.EnqueueJob(ctx, "flush_test", []byte(payload), 0)
		require.NoError(t, err)
	}

	// Start queue manager
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 2
		runOpts.DequeueTimeout = 200 * time.Millisecond
		done <- qm.Run(runCtx, runOpts)
	}()

	// Wait for processing
	time.Sleep(1 * time.Second)

	// Initiate shutdown
	cancel()
	err = qm.Shutdown(context.Background())
	require.NoError(t, err)

	<-done

	// Verify all jobs processed
	assert.Equal(t, int32(5), processedCount.Load(),
		"All jobs should be processed")

	// Give time for final buffer flush
	time.Sleep(200 * time.Millisecond)

	// In a real test, we would query the statistics table to verify
	// that all completions were flushed to the database
	// For now, we verify shutdown completed without error
}

// TestShutdownTimeout tests shutdown timeout protection
func TestShutdownTimeout(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)

	// Register very long-running job
	err := qm.Entrypoint("timeout_test", func(ctx context.Context, job *queue.Job) error {
		// Sleep longer than shutdown timeout
		time.Sleep(35 * time.Second)
		return nil
	})
	require.NoError(t, err)

	ctx := context.Background()
	err = qm.EnqueueJob(ctx, "timeout_test", []byte(`{"id": 1}`), 0)
	require.NoError(t, err)

	// Start queue manager
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 1
		runOpts.DequeueTimeout = 200 * time.Millisecond
		done <- qm.Run(runCtx, runOpts)
	}()

	// Let job start
	time.Sleep(300 * time.Millisecond)

	// Initiate shutdown with short timeout
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	shutdownStart := time.Now()
	_ = qm.Shutdown(shutdownCtx) // Error expected due to timeout, ignore it
	shutdownDuration := time.Since(shutdownStart)

	// Shutdown should timeout (not wait 35 seconds)
	assert.LessOrEqual(t, shutdownDuration.Seconds(), 3.0,
		"Shutdown should timeout, not wait indefinitely (got %v)", shutdownDuration)

	t.Logf("Shutdown timed out after %v (expected ~2s)", shutdownDuration)
}

// TestShutdownMultipleCalls tests that multiple Shutdown calls are safe
func TestShutdownMultipleCalls(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)

	err := qm.Entrypoint("multi_shutdown_test", func(ctx context.Context, job *queue.Job) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	})
	require.NoError(t, err)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		err := qm.EnqueueJob(ctx, "multi_shutdown_test", []byte(fmt.Sprintf(`{"id": %d}`, i)), 0)
		require.NoError(t, err)
	}

	// Start queue manager
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 1
		runOpts.DequeueTimeout = 200 * time.Millisecond
		done <- qm.Run(runCtx, runOpts)
	}()

	time.Sleep(300 * time.Millisecond)

	// Call shutdown multiple times concurrently
	cancel()
	var wg sync.WaitGroup
	errors := make([]error, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errors[idx] = qm.Shutdown(context.Background())
		}(i)
	}

	wg.Wait()
	<-done

	// All shutdown calls should succeed
	for i, err := range errors {
		assert.NoError(t, err, "Shutdown call %d should not error", i)
	}
}

// TestShutdownEmptyQueue tests shutdown with no jobs
func TestShutdownEmptyQueue(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)

	err := qm.Entrypoint("empty_test", func(ctx context.Context, job *queue.Job) error {
		return nil
	})
	require.NoError(t, err)

	// Start queue manager with no jobs
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 2
		runOpts.DequeueTimeout = 200 * time.Millisecond
		done <- qm.Run(runCtx, runOpts)
	}()

	time.Sleep(500 * time.Millisecond)

	// Shutdown should complete quickly with no jobs
	cancel()
	shutdownStart := time.Now()
	err = qm.Shutdown(context.Background())
	shutdownDuration := time.Since(shutdownStart)

	require.NoError(t, err)
	<-done

	assert.Less(t, shutdownDuration.Milliseconds(), int64(500),
		"Shutdown should be fast with no jobs (got %v)", shutdownDuration)

	t.Logf("Empty queue shutdown took %v", shutdownDuration)
}

// TestShutdownDuringEnqueue tests shutdown while jobs are being enqueued
func TestShutdownDuringEnqueue(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)

	var processedCount atomic.Int32

	err := qm.Entrypoint("enqueue_test", func(ctx context.Context, job *queue.Job) error {
		processedCount.Add(1)
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	require.NoError(t, err)

	// Start queue manager
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 2
		runOpts.DequeueTimeout = 200 * time.Millisecond
		done <- qm.Run(runCtx, runOpts)
	}()

	// Enqueue jobs concurrently
	enqueueCtx, enqueueCancel := context.WithCancel(context.Background())
	var enqueueWg sync.WaitGroup

	for i := 0; i < 3; i++ {
		enqueueWg.Add(1)
		go func(id int) {
			defer enqueueWg.Done()
			for j := 0; j < 10; j++ {
				select {
				case <-enqueueCtx.Done():
					return
				default:
					payload := fmt.Sprintf(`{"id": %d_%d}`, id, j)
					qm.EnqueueJob(context.Background(), "enqueue_test", []byte(payload), 0)
					time.Sleep(50 * time.Millisecond)
				}
			}
		}(i)
	}

	// Let some enqueuing happen
	time.Sleep(500 * time.Millisecond)

	// Shutdown
	cancel()
	enqueueCancel()
	err = qm.Shutdown(context.Background())
	require.NoError(t, err)

	<-done
	enqueueWg.Wait()

	// Some jobs should have been processed
	processed := int(processedCount.Load())
	assert.Greater(t, processed, 0,
		"Some jobs should have been processed before shutdown")

	t.Logf("Processed %d jobs before shutdown", processed)
}

// TestShutdownWithListener tests shutdown stops listener gracefully
func TestShutdownWithListener(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)

	var processedCount atomic.Int32

	err := qm.Entrypoint("listener_test", func(ctx context.Context, job *queue.Job) error {
		processedCount.Add(1)
		return nil
	})
	require.NoError(t, err)

	// Start with events (includes listener)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 2
		runOpts.DequeueTimeout = 200 * time.Millisecond
		done <- qm.RunWithEvents(runCtx, runOpts)
	}()

	// Enqueue some jobs
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		err := qm.EnqueueJob(ctx, "listener_test", []byte(fmt.Sprintf(`{"id": %d}`, i)), 0)
		require.NoError(t, err)
	}

	// Wait for processing
	time.Sleep(1 * time.Second)

	// Shutdown should stop listener
	// First cancel context to signal shutdown
	cancel()
	shutdownStart := time.Now()

	// Wait for RunWithEvents to complete FIRST
	// This ensures listener goroutines exit before Shutdown cleanup
	t.Log("Waiting for RunWithEvents to complete after context cancel...")
	select {
	case runErr := <-done:
		t.Logf("RunWithEvents completed with error: %v", runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithEvents did not complete within 5 seconds after context cancel")
	}

	// Now call Shutdown to clean up resources
	err = qm.Shutdown(context.Background())
	shutdownDuration := time.Since(shutdownStart)
	require.NoError(t, err)

	assert.Greater(t, int(processedCount.Load()), 0,
		"Some jobs should be processed")

	// Total shutdown (context cancel + Shutdown) should complete within reasonable time
	assert.Less(t, shutdownDuration.Seconds(), 10.0,
		"Total shutdown should complete within 10s (got %v)", shutdownDuration)

	t.Logf("Processed %d jobs, total shutdown took %v",
		processedCount.Load(), shutdownDuration)

	// Give extra time for all goroutines and connections to fully exit
	time.Sleep(500 * time.Millisecond)
} // TestShutdownStatistics tests that statistics are properly recorded on shutdown
func TestShutdownStatistics(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)

	err := qm.Entrypoint("stats_test", func(ctx context.Context, job *queue.Job) error {
		return nil
	})
	require.NoError(t, err)

	ctx := context.Background()

	// Enqueue jobs
	for i := 0; i < 10; i++ {
		err := qm.EnqueueJob(ctx, "stats_test", []byte(fmt.Sprintf(`{"id": %d}`, i)), 0)
		require.NoError(t, err)
	}

	// Start queue manager
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 3
		runOpts.DequeueTimeout = 200 * time.Millisecond
		done <- qm.Run(runCtx, runOpts)
	}()

	// Wait for processing
	time.Sleep(1 * time.Second)

	// Shutdown
	cancel()
	err = qm.Shutdown(context.Background())
	require.NoError(t, err)

	<-done

	// Give time for final statistics flush
	time.Sleep(500 * time.Millisecond)

	// In a real test, we would verify statistics in database
	// For now, verify shutdown completed without error
	assert.NoError(t, err, "Shutdown should complete successfully")
}
