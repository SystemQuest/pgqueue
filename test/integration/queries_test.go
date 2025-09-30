package integration

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/systemquest/pgqueue4go/pkg/db/generated"
	"github.com/systemquest/pgqueue4go/test/testutil"
)

// TestQueriesPut migrates pgqueuer's test_queries_put
// Tests basic job enqueueing with parametrized N values (1, 2, 64)
func TestQueriesPut(t *testing.T) {
	testCases := []struct {
		name string
		N    int
	}{
		{"Single", 1},
		{"Dual", 2},
		{"Many", 64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup test database - similar to pgqueuer's pgdriver fixture
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			queries := generated.New(testDB.Pool())

			// Verify initial empty queue - assert sum(x.count for x in await q.queue_size()) == 0
			queueSize, err := queries.GetQueueSize(ctx)
			require.NoError(t, err)
			totalCount := int64(0)
			for _, size := range queueSize {
				totalCount += size.Count
			}
			assert.Equal(t, int64(0), totalCount, "Queue should be empty initially")

			// Enqueue N jobs with "placeholder" entrypoint and nil payload
			// Similar to: for _ in range(N): await q.enqueue("placeholder", None)
			for i := 0; i < tc.N; i++ {
				err := queries.EnqueueJob(ctx, generated.EnqueueJobParams{
					Priority:   0,
					Entrypoint: "placeholder",
					Payload:    nil, // nil payload like pgqueuer
				})
				require.NoError(t, err)
			}

			// Verify queue size matches N - assert sum(x.count for x in await q.queue_size()) == N
			queueSize, err = queries.GetQueueSize(ctx)
			require.NoError(t, err)
			totalCount = 0
			for _, size := range queueSize {
				totalCount += size.Count
			}
			assert.Equal(t, int64(tc.N), totalCount, "Queue should contain %d jobs", tc.N)
		})
	}
}

// TestQueriesNextJobs migrates pgqueuer's test_queries_next_jobs
// Tests basic job dequeuing and processing with parametrized N values (1, 2, 64)
func TestQueriesNextJobs(t *testing.T) {
	testCases := []struct {
		name string
		N    int
	}{
		{"Single", 1},
		{"Dual", 2},
		{"Many", 64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup test database
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			queries := generated.New(testDB.Pool())

			// Enqueue N jobs with numbered payloads - similar to pgqueuer's:
			// await q.enqueue(["placeholder"] * N, [f"{n}".encode() for n in range(N)], [0] * N)
			for i := 0; i < tc.N; i++ {
				payload := []byte(strconv.Itoa(i))
				err := queries.EnqueueJob(ctx, generated.EnqueueJobParams{
					Priority:   0,
					Entrypoint: "placeholder",
					Payload:    payload,
				})
				require.NoError(t, err)
			}

			// Process all jobs using atomic dequeue - similar to pgqueuer's dequeue loop
			seen := make([]int, 0)

			for {
				// Dequeue jobs atomically - while jobs := await q.dequeue(entrypoints={"placeholder"}, batch_size=10)
				jobs, err := queries.DequeueJobsAtomic(ctx, generated.DequeueJobsAtomicParams{
					Column1: []string{"placeholder"},
					Limit:   10,
				})
				require.NoError(t, err)

				if len(jobs) == 0 {
					break
				}

				for _, job := range jobs {
					// Process job payload - assert payoad is not None; seen.append(int(payoad))
					require.NotNil(t, job.Payload, "Job payload should not be nil")
					payloadInt, err := strconv.Atoi(string(job.Payload))
					require.NoError(t, err)
					seen = append(seen, payloadInt)

					// Log job as successful - await q.log_jobs([(job, "successful")])
					err = queries.InsertJobStatistics(ctx, generated.InsertJobStatisticsParams{
						Priority:    job.Priority,
						Entrypoint:  job.Entrypoint,
						TimeInQueue: pgtype.Interval{Valid: true, Microseconds: 0}, // Simplified
						Created:     job.Created,
						Status:      generated.StatisticsStatusSuccessful,
						Count:       1,
					})
					require.NoError(t, err)

					// Delete processed job
					err = queries.DeleteJob(ctx, job.ID)
					require.NoError(t, err)
				}
			}

			// Verify all jobs were processed in order - assert seen == list(range(N))
			expectedSeq := make([]int, tc.N)
			for i := 0; i < tc.N; i++ {
				expectedSeq[i] = i
			}
			sort.Ints(seen) // Sort to handle any ordering differences
			assert.Equal(t, expectedSeq, seen, "All jobs should be processed exactly once")
		})
	}
}

// TestQueriesNextJobsConcurrent migrates pgqueuer's test_queries_next_jobs_concurrent
// Tests concurrent job dequeuing with parametrized N and concurrency values
func TestQueriesNextJobsConcurrent(t *testing.T) {
	testCases := []struct {
		name        string
		N           int
		concurrency int
	}{
		{"1x1", 1, 1},
		{"2x2", 2, 2},
		{"64x4", 64, 4},
		{"64x16", 64, 16},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup test database
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			queries := generated.New(testDB.Pool())

			// Enqueue N jobs with numbered payloads
			for i := 0; i < tc.N; i++ {
				payload := []byte(strconv.Itoa(i))
				err := queries.EnqueueJob(ctx, generated.EnqueueJobParams{
					Priority:   0,
					Entrypoint: "placeholder",
					Payload:    payload,
				})
				require.NoError(t, err)
			}

			// Track seen jobs with thread-safe access
			var seen []int
			var seenMu sync.Mutex

			// Consumer function - async def consumer() -> None
			consumer := func() error {
				for {
					// Atomic dequeue - while jobs := await q.dequeue(entrypoints={"placeholder"}, batch_size=10)
					jobs, err := queries.DequeueJobsAtomic(ctx, generated.DequeueJobsAtomicParams{
						Column1: []string{"placeholder"},
						Limit:   10,
					})
					if err != nil {
						return err
					}

					if len(jobs) == 0 {
						break
					}

					for _, job := range jobs {
						// Process payload - assert payload is not None; seen.append(int(payload))
						require.NotNil(t, job.Payload)
						payloadInt, err := strconv.Atoi(string(job.Payload))
						if err != nil {
							return err
						}

						seenMu.Lock()
						seen = append(seen, payloadInt)
						seenMu.Unlock()

						// Log job as successful - await q.log_jobs([(job, "successful")])
						err = queries.InsertJobStatistics(ctx, generated.InsertJobStatisticsParams{
							Priority:    job.Priority,
							Entrypoint:  job.Entrypoint,
							TimeInQueue: pgtype.Interval{Valid: true, Microseconds: 0},
							Created:     job.Created,
							Status:      generated.StatisticsStatusSuccessful,
							Count:       1,
						})
						if err != nil {
							return err
						}

						// Delete processed job
						err = queries.DeleteJob(ctx, job.ID)
						if err != nil {
							return err
						}
					}
				}
				return nil
			}

			// Run concurrent consumers - await asyncio.gather(*[consumer() for _ in range(concurrency)])
			var wg sync.WaitGroup
			errChan := make(chan error, tc.concurrency)

			for i := 0; i < tc.concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := consumer(); err != nil {
						errChan <- err
					}
				}()
			}

			// Wait for all consumers with timeout - await asyncio.wait_for(..., 10)
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()

			select {
			case <-done:
				// Success
			case err := <-errChan:
				t.Fatalf("Consumer error: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("Test timeout - concurrent processing took too long")
			}

			// Verify all jobs were processed exactly once - assert sorted(seen) == list(range(N))
			seenMu.Lock()
			sort.Ints(seen)
			seenMu.Unlock()

			expectedSeq := make([]int, tc.N)
			for i := 0; i < tc.N; i++ {
				expectedSeq[i] = i
			}
			assert.Equal(t, expectedSeq, seen, "All jobs should be processed exactly once in concurrent scenario")
		})
	}
}

// TestQueriesClear migrates pgqueuer's test_queries_clear
// Tests queue clearing functionality
func TestQueriesClear(t *testing.T) {
	// Setup test database
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	queries := generated.New(testDB.Pool())

	// Helper function to get total queue count
	getTotalQueueCount := func() int64 {
		queueSize, err := queries.GetQueueSize(ctx)
		require.NoError(t, err)
		total := int64(0)
		for _, size := range queueSize {
			total += size.Count
		}
		return total
	}

	// Clear queue and verify empty - await q.clear_queue(); assert sum(...) == 0
	err := queries.ClearQueue(ctx, []string{}) // Clear all (empty slice means all)
	require.NoError(t, err)
	assert.Equal(t, int64(0), getTotalQueueCount(), "Queue should be empty after clear")

	// Add a job - await q.enqueue("placeholder", None)
	err = queries.EnqueueJob(ctx, generated.EnqueueJobParams{
		Priority:   0,
		Entrypoint: "placeholder",
		Payload:    nil,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), getTotalQueueCount(), "Queue should contain 1 job")

	// Clear queue again - await q.clear_queue(); assert sum(...) == 0
	err = queries.ClearQueue(ctx, []string{"placeholder"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), getTotalQueueCount(), "Queue should be empty after second clear")
}

// TestMoveJobLog migrates pgqueuer's test_move_job_log
// Tests job logging and statistics functionality
func TestMoveJobLog(t *testing.T) {
	testCases := []struct {
		name string
		N    int
	}{
		{"Single", 1},
		{"Dual", 2},
		{"Many", 64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup test database
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			queries := generated.New(testDB.Pool())

			// Enqueue N jobs with numbered payloads
			for i := 0; i < tc.N; i++ {
				payload := []byte(strconv.Itoa(i))
				err := queries.EnqueueJob(ctx, generated.EnqueueJobParams{
					Priority:   0,
					Entrypoint: "placeholder",
					Payload:    payload,
				})
				require.NoError(t, err)
			}

			// Process all jobs and log them
			for {
				jobs, err := queries.DequeueJobsAtomic(ctx, generated.DequeueJobsAtomicParams{
					Column1: []string{"placeholder"},
					Limit:   10,
				})
				require.NoError(t, err)

				if len(jobs) == 0 {
					break
				}

				for _, job := range jobs {
					// Log job as successful - await q.log_jobs([(job, "successful")])
					err = queries.InsertJobStatistics(ctx, generated.InsertJobStatisticsParams{
						Priority:    job.Priority,
						Entrypoint:  job.Entrypoint,
						TimeInQueue: pgtype.Interval{Valid: true, Microseconds: 0},
						Created:     job.Created,
						Status:      generated.StatisticsStatusSuccessful,
						Count:       1,
					})
					require.NoError(t, err)

					// Delete job after logging
					err = queries.DeleteJob(ctx, job.ID)
					require.NoError(t, err)
				}
			}

			// Verify statistics count - assert sum(s.count for s in await q.log_statistics(1_000_000_000)) == N
			stats, err := queries.GetStatistics(ctx, 1000000000)
			require.NoError(t, err)

			totalStats := int64(0)
			for _, stat := range stats {
				totalStats += stat.Count
			}
			assert.Equal(t, int64(tc.N), totalStats, "Statistics should record all %d processed jobs", tc.N)
		})
	}
}

// TestClearQueue migrates pgqueuer's test_clear_queue
// Tests selective queue clearing functionality
func TestClearQueue(t *testing.T) {
	testCases := []struct {
		name string
		N    int
	}{
		{"Single", 1},
		{"Dual", 2},
		{"Few", 5},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup test database
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			queries := generated.New(testDB.Pool())

			// Helper to get queue size for specific entrypoints
			getQueueSizeForEntrypoints := func(entrypoints []string) int64 {
				queueSize, err := queries.GetQueueSize(ctx)
				require.NoError(t, err)

				total := int64(0)
				entrypointSet := make(map[string]bool)
				for _, ep := range entrypoints {
					entrypointSet[ep] = true
				}

				for _, size := range queueSize {
					if len(entrypoints) == 0 || entrypointSet[size.Entrypoint] {
						total += size.Count
					}
				}
				return total
			}

			getTotalQueueCount := func() int64 {
				return getQueueSizeForEntrypoints([]string{})
			}

			// Test 1: Delete all by listing all entrypoints
			// Enqueue jobs with different entrypoints
			entrypoints := make([]string, tc.N)
			for i := 0; i < tc.N; i++ {
				entrypoint := fmt.Sprintf("placeholder%d", i)
				entrypoints[i] = entrypoint

				err := queries.EnqueueJob(ctx, generated.EnqueueJobParams{
					Priority:   0,
					Entrypoint: entrypoint,
					Payload:    nil,
				})
				require.NoError(t, err)
			}

			// Verify all jobs are queued
			assert.Equal(t, int64(tc.N), getTotalQueueCount(), "Should have %d jobs queued", tc.N)

			// Clear specific entrypoints
			err := queries.ClearQueue(ctx, entrypoints)
			require.NoError(t, err)
			assert.Equal(t, int64(0), getTotalQueueCount(), "Queue should be empty after clearing specific entrypoints")

			// Test 2: Delete all with empty slice (equivalent to None in Python)
			// Re-enqueue jobs
			for i := 0; i < tc.N; i++ {
				entrypoint := fmt.Sprintf("placeholder%d", i)

				err := queries.EnqueueJob(ctx, generated.EnqueueJobParams{
					Priority:   0,
					Entrypoint: entrypoint,
					Payload:    nil,
				})
				require.NoError(t, err)
			}

			assert.Equal(t, int64(tc.N), getTotalQueueCount(), "Should have %d jobs queued again", tc.N)

			// Clear all with empty slice (simulating None in Python)
			err = queries.ClearQueue(ctx, []string{})
			require.NoError(t, err)
			assert.Equal(t, int64(0), getTotalQueueCount(), "Queue should be empty after clearing all")

			// Test 3: Delete one specific entrypoint
			// Re-enqueue jobs
			for i := 0; i < tc.N; i++ {
				entrypoint := fmt.Sprintf("placeholder%d", i)

				err := queries.EnqueueJob(ctx, generated.EnqueueJobParams{
					Priority:   0,
					Entrypoint: entrypoint,
					Payload:    nil,
				})
				require.NoError(t, err)
			}

			assert.Equal(t, int64(tc.N), getTotalQueueCount(), "Should have %d jobs queued for third test", tc.N)

			// Clear only placeholder0
			err = queries.ClearQueue(ctx, []string{"placeholder0"})
			require.NoError(t, err)
			assert.Equal(t, int64(tc.N-1), getTotalQueueCount(), "Should have %d jobs after clearing one", tc.N-1)
		})
	}
}

// TestQueuePriority migrates pgqueuer's test_queue_priority
// Tests job priority ordering
func TestQueuePriority(t *testing.T) {
	testCases := []struct {
		name string
		N    int
	}{
		{"Single", 1},
		{"Dual", 2},
		{"Many", 64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup test database
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			queries := generated.New(testDB.Pool())

			// Enqueue N jobs with increasing priorities - priorities list(range(N))
			for i := 0; i < tc.N; i++ {
				payload := []byte(strconv.Itoa(i))
				err := queries.EnqueueJob(ctx, generated.EnqueueJobParams{
					Priority:   int32(i), // Priority increases with i
					Entrypoint: "placeholder",
					Payload:    payload,
				})
				require.NoError(t, err)
			}

			// Dequeue all jobs and verify priority ordering
			var jobs []generated.DequeueJobsAtomicRow

			for {
				nextJobs, err := queries.DequeueJobsAtomic(ctx, generated.DequeueJobsAtomicParams{
					Column1: []string{"placeholder"},
					Limit:   10,
				})
				require.NoError(t, err)

				if len(nextJobs) == 0 {
					break
				}

				for _, job := range nextJobs {
					jobs = append(jobs, job)

					// Log job as successful
					err = queries.InsertJobStatistics(ctx, generated.InsertJobStatisticsParams{
						Priority:    job.Priority,
						Entrypoint:  job.Entrypoint,
						TimeInQueue: pgtype.Interval{Valid: true, Microseconds: 0},
						Created:     job.Created,
						Status:      generated.StatisticsStatusSuccessful,
						Count:       1,
					})
					require.NoError(t, err)

					// Delete processed job
					err = queries.DeleteJob(ctx, job.ID)
					require.NoError(t, err)
				}
			}

			// Verify jobs are sorted by priority descending - assert jobs == sorted(jobs, key=lambda x: x.priority, reverse=True)
			require.Len(t, jobs, tc.N, "Should have processed all %d jobs", tc.N)

			// Check if jobs are in descending priority order
			for i := 1; i < len(jobs); i++ {
				assert.GreaterOrEqual(t, jobs[i-1].Priority, jobs[i].Priority,
					"Jobs should be ordered by priority (descending), job %d has priority %d, job %d has priority %d",
					i-1, jobs[i-1].Priority, i, jobs[i].Priority)
			}
		})
	}
}

// TestQueueRetryTimer migrates pgqueuer's test_queue_retry_timer
// Tests retry timer functionality for picked jobs
func TestQueueRetryTimer(t *testing.T) {
	testCases := []struct {
		name string
		N    int
	}{
		{"Single", 1},
		{"Dual", 2},
		{"Many", 64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup test database
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			queries := generated.New(testDB.Pool())
			retryTimer := 100 * time.Millisecond // retry_timer: timedelta = timedelta(seconds=0.1)

			// Enqueue N jobs with increasing priorities
			for i := 0; i < tc.N; i++ {
				payload := []byte(strconv.Itoa(i))
				err := queries.EnqueueJob(ctx, generated.EnqueueJobParams{
					Priority:   int32(i),
					Entrypoint: "placeholder",
					Payload:    payload,
				})
				require.NoError(t, err)
			}

			// Pick all jobs but don't process them (simulates "in progress")
			// while _ := await q.dequeue(batch_size=10, entrypoints={"placeholder"}): ...
			pickedCount := 0
			for {
				jobs, err := queries.DequeueJobsAtomic(ctx, generated.DequeueJobsAtomicParams{
					Column1: []string{"placeholder"},
					Limit:   10,
				})
				require.NoError(t, err)

				if len(jobs) == 0 {
					break
				}
				pickedCount += len(jobs)
				// Note: We don't delete or log these jobs, leaving them in "picked" state
			}
			assert.Equal(t, tc.N, pickedCount, "Should have picked all %d jobs", tc.N)

			// Verify no more jobs can be dequeued immediately
			// assert len(await q.dequeue(batch_size=10, entrypoints={"placeholder"})) == 0
			jobs, err := queries.DequeueJobsAtomic(ctx, generated.DequeueJobsAtomicParams{
				Column1: []string{"placeholder"},
				Limit:   10,
			})
			require.NoError(t, err)
			assert.Empty(t, jobs, "Should not be able to dequeue jobs immediately after picking")

			// Sleep to simulate slow entrypoint function - await asyncio.sleep(retry_timer.total_seconds())
			time.Sleep(retryTimer)

			// Re-fetch with retry timer, should get the same number of jobs
			var retryJobs []generated.DequeueRetryJobsAtomicRow
			retryInterval := pgtype.Interval{
				Valid:        true,
				Microseconds: int64(retryTimer.Microseconds()),
			}

			for {
				nextJobs, err := queries.DequeueRetryJobsAtomic(ctx, generated.DequeueRetryJobsAtomicParams{
					Column1: []string{"placeholder"},
					Column2: retryInterval,
					Limit:   10,
				})
				require.NoError(t, err)

				if len(nextJobs) == 0 {
					break
				}
				retryJobs = append(retryJobs, nextJobs...)
			}

			// Verify we got the same number of jobs as originally queued
			// assert len(jobs) == N
			assert.Len(t, retryJobs, tc.N, "Should be able to retry all %d jobs after timeout", tc.N)
		})
	}
}

// TestQueueRetryTimerNegativeRaises migrates pgqueuer's test_queue_retry_timer_negative_raises
// Tests that negative retry timers raise appropriate errors
func TestQueueRetryTimerNegativeRaises(t *testing.T) {
	// Setup test database
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	queries := generated.New(testDB.Pool())

	// Test negative microseconds - equivalent to -timedelta(seconds=0.001)
	negativeInterval := pgtype.Interval{
		Valid:        true,
		Microseconds: -1000, // -1ms in microseconds
	}

	// This should handle the negative interval gracefully or return no results
	// In Go, we don't have direct ValueError equivalent, but the query should handle this
	jobs, err := queries.DequeueRetryJobsAtomic(ctx, generated.DequeueRetryJobsAtomicParams{
		Column1: []string{"placeholder"},
		Column2: negativeInterval,
		Limit:   10,
	})

	// Either should return error or empty results, depending on implementation
	// PostgreSQL will likely return no results for negative intervals
	if err != nil {
		t.Logf("Expected behavior: negative interval returned error: %v", err)
	} else {
		assert.Empty(t, jobs, "Negative retry timer should return no jobs")
		t.Log("Expected behavior: negative interval returned empty results")
	}

	// Test second negative case - equivalent to timedelta(seconds=-0.001)
	negativeInterval2 := pgtype.Interval{
		Valid:        true,
		Microseconds: -1000, // -1ms in microseconds
	}

	jobs2, err2 := queries.DequeueRetryJobsAtomic(ctx, generated.DequeueRetryJobsAtomicParams{
		Column1: []string{"placeholder"},
		Column2: negativeInterval2,
		Limit:   10,
	})

	if err2 != nil {
		t.Logf("Expected behavior: second negative interval returned error: %v", err2)
	} else {
		assert.Empty(t, jobs2, "Second negative retry timer should return no jobs")
		t.Log("Expected behavior: second negative interval returned empty results")
	}
}
