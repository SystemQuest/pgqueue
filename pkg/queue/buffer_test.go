package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatisticsBufferFlush tests size-based flushing
// Aligns with PgQueuer's buffer behavior: flush when max_size is reached
func TestStatisticsBufferFlush(t *testing.T) {
	testCases := []struct {
		name        string
		maxSize     int
		addCount    int
		wantFlushes int
	}{
		{
			name:        "SingleFlush",
			maxSize:     10,
			addCount:    10,
			wantFlushes: 1,
		},
		{
			name:        "MultipleFlushes",
			maxSize:     5,
			addCount:    13,
			wantFlushes: 2, // 5, 5, then 3 remaining
		},
		{
			name:        "NoFlush",
			maxSize:     100,
			addCount:    10,
			wantFlushes: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var flushCount atomic.Int32
			var flushedEvents []JobCompletion
			var mu sync.Mutex

			callback := func(ctx context.Context, events []JobCompletion) error {
				flushCount.Add(1)
				mu.Lock()
				flushedEvents = append(flushedEvents, events...)
				mu.Unlock()
				return nil
			}

			buffer := NewStatisticsBuffer(tc.maxSize, 1*time.Hour, callback, slog.Default())
			defer buffer.Stop()

			// Add jobs
			for i := 0; i < tc.addCount; i++ {
				job := &Job{ID: int32(i), Entrypoint: "test"}
				err := buffer.Add(JobCompletion{Job: job, Status: StatisticsStatusSuccessful})
				require.NoError(t, err)
			}

			// Small delay to ensure flush completes
			time.Sleep(100 * time.Millisecond)

			assert.Equal(t, tc.wantFlushes, int(flushCount.Load()),
				"Expected %d flushes, got %d", tc.wantFlushes, flushCount.Load())

			mu.Lock()
			flushedCount := len(flushedEvents)
			mu.Unlock()

			expectedFlushed := (tc.addCount / tc.maxSize) * tc.maxSize
			assert.Equal(t, expectedFlushed, flushedCount,
				"Expected %d flushed events, got %d", expectedFlushed, flushedCount)
		})
	}
}

// TestStatisticsBufferTimeout tests timeout-based flushing
// Aligns with PgQueuer's buffer behavior: flush after timeout if events exist
func TestStatisticsBufferTimeout(t *testing.T) {
	var flushCount atomic.Int32
	var flushedEvents []JobCompletion
	var mu sync.Mutex

	callback := func(ctx context.Context, events []JobCompletion) error {
		flushCount.Add(1)
		mu.Lock()
		flushedEvents = append(flushedEvents, events...)
		mu.Unlock()
		return nil
	}

	timeout := 200 * time.Millisecond
	buffer := NewStatisticsBuffer(100, timeout, callback, slog.Default())
	defer buffer.Stop()

	// Add a few jobs (less than maxSize)
	for i := 0; i < 5; i++ {
		job := &Job{ID: int32(i), Entrypoint: "test"}
		err := buffer.Add(JobCompletion{Job: job, Status: StatisticsStatusSuccessful})
		require.NoError(t, err)
	}

	// Wait for timeout to trigger flush
	time.Sleep(timeout + 100*time.Millisecond)

	// Should have flushed due to timeout
	assert.GreaterOrEqual(t, int(flushCount.Load()), 1,
		"Expected at least 1 timeout-based flush")

	mu.Lock()
	flushedCount := len(flushedEvents)
	mu.Unlock()

	assert.Equal(t, 5, flushedCount, "Expected 5 flushed events, got %d", flushedCount)
}

// TestStatisticsBufferConcurrent tests concurrent access to buffer
// Ensures thread-safety like PgQueuer's asyncio-safe buffer
func TestStatisticsBufferConcurrent(t *testing.T) {
	var flushCount atomic.Int32
	var totalFlushed atomic.Int32

	callback := func(ctx context.Context, events []JobCompletion) error {
		flushCount.Add(1)
		totalFlushed.Add(int32(len(events)))
		return nil
	}

	buffer := NewStatisticsBuffer(10, 1*time.Second, callback, slog.Default())
	defer buffer.Stop()

	// Add jobs concurrently from multiple goroutines
	const numGoroutines = 10
	const jobsPerGoroutine = 20
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < jobsPerGoroutine; j++ {
				job := &Job{
					ID:         int32(id*jobsPerGoroutine + j),
					Entrypoint: fmt.Sprintf("test_%d", id),
				}
				buffer.Add(JobCompletion{Job: job, Status: StatisticsStatusSuccessful})
			}
		}(i)
	}

	wg.Wait()

	// Wait for final flush
	buffer.Stop()
	time.Sleep(100 * time.Millisecond)

	totalAdded := numGoroutines * jobsPerGoroutine
	assert.Equal(t, totalAdded, int(totalFlushed.Load()),
		"Expected %d total flushed events, got %d", totalAdded, totalFlushed.Load())

	// Should have multiple flushes due to buffer size
	expectedFlushes := totalAdded / 10
	assert.GreaterOrEqual(t, int(flushCount.Load()), expectedFlushes,
		"Expected at least %d flushes", expectedFlushes)
}

// TestStatisticsBufferStop tests graceful shutdown with final flush
// Aligns with PgQueuer's buffer.alive = False behavior
func TestStatisticsBufferStop(t *testing.T) {
	var flushedEvents []JobCompletion
	var mu sync.Mutex

	callback := func(ctx context.Context, events []JobCompletion) error {
		mu.Lock()
		flushedEvents = append(flushedEvents, events...)
		mu.Unlock()
		return nil
	}

	buffer := NewStatisticsBuffer(100, 1*time.Hour, callback, slog.Default())

	// Add jobs without triggering size-based flush
	for i := 0; i < 15; i++ {
		job := &Job{ID: int32(i), Entrypoint: "test"}
		err := buffer.Add(JobCompletion{Job: job, Status: StatisticsStatusSuccessful})
		require.NoError(t, err)
	}

	// Stop should trigger final flush
	err := buffer.Stop()
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	flushedCount := len(flushedEvents)
	mu.Unlock()

	assert.Equal(t, 15, flushedCount,
		"Expected final flush to include all 15 events, got %d", flushedCount)
}

// TestStatisticsBufferErrorHandling tests error handling in flush callback
func TestStatisticsBufferErrorHandling(t *testing.T) {
	var callCount atomic.Int32
	var lastError error

	callback := func(ctx context.Context, events []JobCompletion) error {
		callCount.Add(1)
		lastError = fmt.Errorf("simulated flush error")
		return lastError
	}

	buffer := NewStatisticsBuffer(5, 1*time.Hour, callback, slog.Default())
	defer buffer.Stop()

	// Add enough jobs to trigger flush
	for i := 0; i < 5; i++ {
		job := &Job{ID: int32(i), Entrypoint: "test"}
		err := buffer.Add(JobCompletion{Job: job, Status: StatisticsStatusSuccessful})
		// First flush will succeed (from Add), but callback returns error
		if i == 4 {
			assert.Error(t, err, "Expected error from flush callback")
		}
	}

	assert.GreaterOrEqual(t, int(callCount.Load()), 1, "Callback should be called")
	assert.Error(t, lastError, "Should have captured error")
}

// TestStatisticsBufferStatuses tests different job completion statuses
// Aligns with PgQueuer's StatisticsStatus enum
func TestStatisticsBufferStatuses(t *testing.T) {
	statusCounts := make(map[StatisticsStatus]int)
	var mu sync.Mutex

	callback := func(ctx context.Context, events []JobCompletion) error {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range events {
			statusCounts[event.Status]++
		}
		return nil
	}

	buffer := NewStatisticsBuffer(20, 1*time.Hour, callback, slog.Default())
	defer buffer.Stop()

	// Add jobs with different statuses
	statuses := []StatisticsStatus{
		StatisticsStatusSuccessful,
		StatisticsStatusException,
		StatisticsStatusSuccessful,
		StatisticsStatusException,
	}

	for i := 0; i < 20; i++ {
		job := &Job{ID: int32(i), Entrypoint: "test"}
		status := statuses[i%len(statuses)]
		err := buffer.Add(JobCompletion{Job: job, Status: status})
		require.NoError(t, err)
	}

	// Trigger flush
	buffer.Stop()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, 10, statusCounts[StatisticsStatusSuccessful])
	assert.Equal(t, 10, statusCounts[StatisticsStatusException])
}

// TestStatisticsBufferEmpty tests that empty buffer doesn't flush
func TestStatisticsBufferEmpty(t *testing.T) {
	var flushCount atomic.Int32

	callback := func(ctx context.Context, events []JobCompletion) error {
		flushCount.Add(1)
		return nil
	}

	timeout := 100 * time.Millisecond
	buffer := NewStatisticsBuffer(10, timeout, callback, slog.Default())

	// Wait for timeout without adding any jobs
	time.Sleep(timeout + 50*time.Millisecond)

	buffer.Stop()

	// Should not have flushed since buffer was empty
	assert.Equal(t, 0, int(flushCount.Load()),
		"Empty buffer should not trigger flush")
}

// BenchmarkStatisticsBufferAdd benchmarks adding events to buffer
func BenchmarkStatisticsBufferAdd(b *testing.B) {
	callback := func(ctx context.Context, events []JobCompletion) error {
		return nil
	}

	buffer := NewStatisticsBuffer(1000, 1*time.Hour, callback, slog.Default())
	defer buffer.Stop()

	job := &Job{ID: 1, Entrypoint: "test"}
	completion := JobCompletion{Job: job, Status: StatisticsStatusSuccessful}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffer.Add(completion)
	}
}

// BenchmarkStatisticsBufferConcurrent benchmarks concurrent access
func BenchmarkStatisticsBufferConcurrent(b *testing.B) {
	callback := func(ctx context.Context, events []JobCompletion) error {
		return nil
	}

	buffer := NewStatisticsBuffer(1000, 1*time.Hour, callback, slog.Default())
	defer buffer.Stop()

	job := &Job{ID: 1, Entrypoint: "test"}
	completion := JobCompletion{Job: job, Status: StatisticsStatusSuccessful}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buffer.Add(completion)
		}
	})
}
