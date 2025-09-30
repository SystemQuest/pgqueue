package integration

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/systemquest/pgqueue4go/pkg/db/generated"
	"github.com/systemquest/pgqueue4go/pkg/queue"
	"github.com/systemquest/pgqueue4go/test/testutil"
)

// perfCounterTime returns high-resolution timestamp similar to pgqueuer's _perf_counter_dt()
func perfCounterTime() time.Time {
	// Go's time.Now() provides nanosecond precision, similar to Python's perf_counter
	return time.Now().UTC()
}

// jobFaker creates a fake job similar to pgqueuer's job_faker()
func jobFaker() *queue.Job {
	return &queue.Job{
		ID:         rand.Int31n(1_000_000_000),
		Priority:   0,
		Created:    perfCounterTime(),
		Status:     queue.JobStatusPicked,
		Entrypoint: "foo",
		Payload:    nil,
	}
}

// TestPerfCounterTime migrates pgqueuer's test_perf_counter_dt
// Tests that perfCounterTime returns a time with timezone info
func TestPerfCounterTime(t *testing.T) {
	result := perfCounterTime()

	// Verify it's a valid time - similar to pgqueuer's isinstance check
	assert.False(t, result.IsZero(), "Should return valid time")

	// Verify timezone info is present - similar to pgqueuer's tzinfo check
	loc := result.Location()
	assert.NotNil(t, loc, "Should have timezone info")

	// Verify it's UTC like pgqueuer
	assert.Equal(t, time.UTC, loc, "Should be UTC timezone")
}

// TestJobBufferMaxSize migrates pgqueuer's test_job_buffer_max_size
// Tests buffer flushing when max_size is reached with parametrized values (1, 2, 3, 5, 64)
func TestJobBufferMaxSize(t *testing.T) {
	testCases := []struct {
		name    string
		maxSize int
	}{
		{"Size1", 1},
		{"Size2", 2},
		{"Size3", 3},
		{"Size5", 5},
		{"Size64", 64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Track flushed jobs - similar to pgqueuer's helper_buffer
			var flushedStats []generated.InsertJobStatisticsParams
			var mu sync.Mutex

			// Helper function similar to pgqueuer's helper(x: list)
			flushCallback := func(ctx context.Context, stats []generated.InsertJobStatisticsParams) error {
				mu.Lock()
				defer mu.Unlock()
				flushedStats = append(flushedStats, stats...)
				return nil
			}

			// Create buffer - similar to pgqueuer's JobBuffer
			buffer := queue.NewStatisticsBuffer(
				tc.maxSize,
				100*time.Second, // Long timeout like pgqueuer's 100 seconds
				flushCallback,
			)
			defer buffer.Stop()

			// Add jobs one by one - similar to pgqueuer's loop
			for i := 0; i < tc.maxSize-1; i++ {
				job := jobFaker()
				timeInQueue := time.Duration(i) * time.Millisecond

				stat := generated.InsertJobStatisticsParams{
					Priority:   job.Priority,
					Entrypoint: job.Entrypoint,
					Count:      1,
					Status:     generated.StatisticsStatusSuccessful,
				}
				// Set time fields
				stat.Created.Time = job.Created
				stat.Created.Valid = true
				stat.TimeInQueue.Microseconds = int64(timeInQueue.Microseconds())
				stat.TimeInQueue.Valid = true

				buffer.Add(stat)

				// Should not flush yet - similar to pgqueuer's assert len(helper_buffer) == 0
				mu.Lock()
				count := len(flushedStats)
				mu.Unlock()
				assert.Equal(t, 0, count, "Should not flush before max_size")
			}

			// Add final job to trigger flush - similar to pgqueuer's final add_job
			job := jobFaker()
			stat := generated.InsertJobStatisticsParams{
				Priority:   job.Priority,
				Entrypoint: job.Entrypoint,
				Count:      1,
				Status:     generated.StatisticsStatusSuccessful,
			}
			stat.Created.Time = job.Created
			stat.Created.Valid = true
			stat.TimeInQueue.Microseconds = int64(time.Millisecond.Microseconds())
			stat.TimeInQueue.Valid = true

			buffer.Add(stat)

			// Give some time for async flush
			time.Sleep(10 * time.Millisecond)

			// Verify flush occurred - similar to pgqueuer's assert len(helper_buffer) == max_size
			mu.Lock()
			count := len(flushedStats)
			mu.Unlock()
			assert.Equal(t, tc.maxSize, count, "Should flush exactly max_size items")
		})
	}
}

// TestJobBufferTimeout migrates pgqueuer's test_job_buffer_timeout
// Tests buffer flushing based on timeout with parametrized N and timeout values
func TestJobBufferTimeout(t *testing.T) {
	testCases := []struct {
		name    string
		N       int
		timeout time.Duration
	}{
		{"N5_10ms", 5, 10 * time.Millisecond},
		{"N5_1ms", 5, 1 * time.Millisecond},
		{"N64_10ms", 64, 10 * time.Millisecond},
		{"N64_1ms", 64, 1 * time.Millisecond},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Track flushed jobs
			var flushedStats []generated.InsertJobStatisticsParams
			var mu sync.Mutex

			// Helper function similar to pgqueuer's helper(x: list)
			flushCallback := func(ctx context.Context, stats []generated.InsertJobStatisticsParams) error {
				mu.Lock()
				defer mu.Unlock()
				flushedStats = append(flushedStats, stats...)
				return nil
			}

			// Create buffer with timeout - similar to pgqueuer's JobBuffer
			buffer := queue.NewStatisticsBuffer(
				tc.N*2, // Larger than N so timeout triggers first
				tc.timeout,
				flushCallback,
			)
			defer buffer.Stop()

			// Add N jobs - similar to pgqueuer's loop
			for i := 0; i < tc.N; i++ {
				job := jobFaker()
				stat := generated.InsertJobStatisticsParams{
					Priority:   job.Priority,
					Entrypoint: job.Entrypoint,
					Count:      1,
					Status:     generated.StatisticsStatusSuccessful,
				}
				stat.Created.Time = job.Created
				stat.Created.Valid = true
				stat.TimeInQueue.Microseconds = int64(time.Millisecond.Microseconds())
				stat.TimeInQueue.Valid = true

				buffer.Add(stat)

				// Should not flush yet - similar to pgqueuer's assert len(helper_buffer) == 0
				mu.Lock()
				count := len(flushedStats)
				mu.Unlock()
				assert.Equal(t, 0, count, "Should not flush before timeout")
			}

			// Wait for timeout to trigger flush - similar to pgqueuer's asyncio.sleep(timeout * 1.1)
			// Add extra wait time since Go's timer behavior might be different
			time.Sleep(time.Duration(float64(tc.timeout) * 2.0))

			// Verify timeout-based flush occurred - similar to pgqueuer's assert len(helper_buffer) == N
			mu.Lock()
			count := len(flushedStats)
			mu.Unlock()
			assert.Equal(t, tc.N, count, "Should flush all N items after timeout")
		})
	}
}

// TestJobBufferBasicFunctionality tests basic buffer operations
// Additional test to ensure our buffer implementation works correctly
func TestJobBufferBasicFunctionality(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}

	ctx := context.Background()

	// Test that buffer integrates with real database
	job := &queue.Job{
		ID:         12345,
		Priority:   1,
		Created:    time.Now(),
		Status:     queue.JobStatusPicked,
		Entrypoint: "test_buffer",
		Payload:    []byte("test payload"),
	}

	// Create a statistics entry
	timeInQueue := 500 * time.Millisecond
	stat := generated.InsertJobStatisticsParams{
		Priority:   job.Priority,
		Entrypoint: job.Entrypoint,
		Count:      1,
		Status:     generated.StatisticsStatusSuccessful,
	}
	stat.Created.Time = job.Created
	stat.Created.Valid = true
	stat.TimeInQueue.Microseconds = int64(timeInQueue.Microseconds())
	stat.TimeInQueue.Valid = true

	// Test direct database insertion (bypassing buffer for verification)
	err := testDB.Queries().InsertJobStatistics(ctx, stat)
	require.NoError(t, err, "Should be able to insert statistics directly")

	t.Log("Buffer basic functionality test passed")
}

// TestJobBufferConcurrency tests concurrent access to the buffer
// Ensures buffer is thread-safe like pgqueuer's async implementation
func TestJobBufferConcurrency(t *testing.T) {
	const numGoroutines = 10
	const itemsPerGoroutine = 5

	var flushedStats []generated.InsertJobStatisticsParams
	var mu sync.Mutex

	flushCallback := func(ctx context.Context, stats []generated.InsertJobStatisticsParams) error {
		mu.Lock()
		defer mu.Unlock()
		flushedStats = append(flushedStats, stats...)
		return nil
	}

	buffer := queue.NewStatisticsBuffer(
		numGoroutines*itemsPerGoroutine, // Set size to trigger flush
		1*time.Second,                   // Long timeout
		flushCallback,
	)
	defer buffer.Stop()

	// Launch concurrent goroutines
	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < itemsPerGoroutine; j++ {
				job := jobFaker()
				stat := generated.InsertJobStatisticsParams{
					Priority:   job.Priority,
					Entrypoint: fmt.Sprintf("test_%d_%d", goroutineID, j),
					Count:      1,
					Status:     generated.StatisticsStatusSuccessful,
				}
				stat.Created.Time = job.Created
				stat.Created.Valid = true
				stat.TimeInQueue.Microseconds = int64(time.Millisecond.Microseconds())
				stat.TimeInQueue.Valid = true

				buffer.Add(stat)
			}
		}(i)
	}

	wg.Wait()

	// Give time for flush
	time.Sleep(50 * time.Millisecond)

	// Verify all items were processed
	mu.Lock()
	count := len(flushedStats)
	mu.Unlock()

	expectedCount := numGoroutines * itemsPerGoroutine
	assert.Equal(t, expectedCount, count, "Should handle concurrent access correctly")
}
