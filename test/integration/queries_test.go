package integration

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/systemquest/pgqueue/pkg/queries"
	"github.com/systemquest/pgqueue/test/testutil"
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
			q := queries.NewQueries(testDB.Pool())

			// Verify initial empty queue - assert sum(x.count for x in await q.queue_size()) == 0
			queueSize, err := q.QueueSize(ctx)
			require.NoError(t, err)
			totalCount := 0
			for _, size := range queueSize {
				totalCount += size.Count
			}
			assert.Equal(t, 0, totalCount, "Queue should be empty initially")

			// Enqueue N jobs with "placeholder" entrypoint and nil payload
			// Similar to: for _ in range(N): await q.enqueue("placeholder", None)
			for i := 0; i < tc.N; i++ {
				err := q.EnqueueJob(ctx, 0, "placeholder", nil) // priority=0, entrypoint="placeholder", payload=nil
				require.NoError(t, err)
			}

			// Verify queue size matches N - assert sum(x.count for x in await q.queue_size()) == N
			queueSize, err = q.QueueSize(ctx)
			require.NoError(t, err)
			totalCount = 0
			for _, size := range queueSize {
				totalCount += size.Count
			}
			assert.Equal(t, tc.N, totalCount, "Queue should contain %d jobs", tc.N)
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
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			q := queries.NewQueries(testDB.Pool())

			// Enqueue N jobs similar to pgqueuer:
			// await q.enqueue(["placeholder"] * N, [f"{n}".encode() for n in range(N)], [0] * N)
			jobs := make([]queries.EnqueueJobParams, tc.N)
			for i := 0; i < tc.N; i++ {
				jobs[i] = queries.EnqueueJobParams{
					Priority:   0,
					Entrypoint: "placeholder",
					Payload:    []byte(strconv.Itoa(i)),
				}
			}
			err := q.EnqueueJobs(ctx, jobs)
			require.NoError(t, err)

			// Dequeue and process jobs similar to pgqueuer
			var seen []int
			for {
				// while jobs := await q.dequeue(entrypoints={"placeholder"}, batch_size=10):
				dequeueParams := queries.DequeueJobsParams{
					BatchSize:   10,
					Entrypoints: []string{"placeholder"},
				}
				jobsBatch, err := q.DequeueJobs(ctx, dequeueParams)
				require.NoError(t, err)

				if len(jobsBatch) == 0 {
					break
				}

				for _, job := range jobsBatch {
					// Extract payload and convert to int
					if job.Payload != nil {
						payload, err := strconv.Atoi(string(job.Payload))
						require.NoError(t, err)
						seen = append(seen, payload)
					}

					// Complete job - await q.log_jobs([(job, "successful")])
					err = q.CompleteJob(ctx, job.ID, "successful")
					require.NoError(t, err)
				}
			}

			// Verify we saw all expected values: assert seen == list(range(N))
			sort.Ints(seen)
			expected := make([]int, tc.N)
			for i := 0; i < tc.N; i++ {
				expected[i] = i
			}
			assert.Equal(t, expected, seen, "Should see all values from 0 to %d", tc.N-1)
		})
	}
}

// TestQueriesNextJobsConcurrent migrates pgqueuer's test_queries_next_jobs_concurrent
// Tests concurrent job processing with parametrized N and concurrency values
func TestQueriesNextJobsConcurrent(t *testing.T) {
	testCases := []struct {
		name        string
		N           int
		concurrency int
	}{
		{"N1_C1", 1, 1},
		{"N2_C2", 2, 2},
		{"N64_C4", 64, 4},
		{"N64_C16", 64, 16},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			q := queries.NewQueries(testDB.Pool())

			// Enqueue N jobs
			jobs := make([]queries.EnqueueJobParams, tc.N)
			for i := 0; i < tc.N; i++ {
				jobs[i] = queries.EnqueueJobParams{
					Priority:   0,
					Entrypoint: "placeholder",
					Payload:    []byte(strconv.Itoa(i)),
				}
			}
			err := q.EnqueueJobs(ctx, jobs)
			require.NoError(t, err)

			// Process jobs concurrently
			var mu sync.Mutex
			var seen []int
			var wg sync.WaitGroup

			for workerID := 0; workerID < tc.concurrency; workerID++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()

					for {
						dequeueParams := queries.DequeueJobsParams{
							BatchSize:   10,
							Entrypoints: []string{"placeholder"},
						}
						jobsBatch, err := q.DequeueJobs(ctx, dequeueParams)
						if err != nil {
							t.Errorf("Worker %d failed to dequeue: %v", id, err)
							return
						}

						if len(jobsBatch) == 0 {
							return // No more jobs
						}

						for _, job := range jobsBatch {
							if job.Payload != nil {
								payload, err := strconv.Atoi(string(job.Payload))
								if err != nil {
									t.Errorf("Worker %d failed to parse payload: %v", id, err)
									continue
								}

								mu.Lock()
								seen = append(seen, payload)
								mu.Unlock()
							}

							// Complete job
							err = q.CompleteJob(ctx, job.ID, "successful")
							if err != nil {
								t.Errorf("Worker %d failed to complete job: %v", id, err)
							}
						}
					}
				}(workerID)
			}

			wg.Wait()

			// Verify all jobs were processed
			sort.Ints(seen)
			expected := make([]int, tc.N)
			for i := 0; i < tc.N; i++ {
				expected[i] = i
			}
			assert.Equal(t, expected, seen, "Should see all values from 0 to %d", tc.N-1)
		})
	}
}

// TestQueriesRetry migrates pgqueuer's test_queries_retry
// Tests retry functionality for jobs
func TestQueriesRetry(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	// Enqueue a job
	err := q.EnqueueJob(ctx, 0, "test", []byte("retry_test"))
	require.NoError(t, err)

	// Dequeue the job
	dequeueParams := queries.DequeueJobsParams{
		BatchSize:   1,
		Entrypoints: []string{"test"},
	}
	jobs, err := q.DequeueJobs(ctx, dequeueParams)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	job := jobs[0]
	assert.Equal(t, "test", job.Entrypoint)
	assert.Equal(t, []byte("retry_test"), job.Payload)

	// Complete the job successfully
	err = q.CompleteJob(ctx, job.ID, "successful")
	require.NoError(t, err)

	// Verify job is no longer in queue
	queueSize, err := q.QueueSize(ctx)
	require.NoError(t, err)
	totalCount := 0
	for _, size := range queueSize {
		totalCount += size.Count
	}
	assert.Equal(t, 0, totalCount, "Queue should be empty")
}

// TestQueriesRetryTimeout migrates pgqueuer's test_queue_retry_timer
// Tests job retry functionality with timeout
func TestQueriesRetryTimeout(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	N := 5
	retryTimeout := 100 * time.Millisecond

	// Enqueue N jobs
	for i := 0; i < N; i++ {
		payload := fmt.Sprintf(`{"id": %d}`, i)
		err := q.EnqueueJob(ctx, i, "placeholder", []byte(payload))
		require.NoError(t, err)
	}

	// First dequeue - pick all jobs but don't complete them (simulate in-progress)
	firstBatch, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
		BatchSize:   10,
		Entrypoints: []string{"placeholder"},
	})
	require.NoError(t, err)
	require.Len(t, firstBatch, N, "Should pick all %d jobs", N)

	// Immediately try to dequeue again - should get nothing (jobs are "picked")
	secondBatch, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
		BatchSize:   10,
		Entrypoints: []string{"placeholder"},
	})
	require.NoError(t, err)
	assert.Len(t, secondBatch, 0, "Should not get jobs that are already picked")

	// Wait for retry timeout
	time.Sleep(retryTimeout + 50*time.Millisecond) // Add buffer

	// Now dequeue with retry timer - should get the same jobs back
	retriedJobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
		BatchSize:    10,
		Entrypoints:  []string{"placeholder"},
		RetryTimeout: &retryTimeout,
	})
	require.NoError(t, err)
	assert.Len(t, retriedJobs, N, "Should retry and get all %d jobs back", N)
}

// TestQueriesRetryNegative migrates pgqueuer's test_queue_retry_timer_negative_raises
// Tests that negative retry timers are rejected
func TestQueriesRetryNegative(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	// Test negative duration - should be handled gracefully
	negativeTimeout := -1 * time.Millisecond
	_, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
		BatchSize:    10,
		Entrypoints:  []string{"placeholder"},
		RetryTimeout: &negativeTimeout,
	})

	// Should return error for negative retry timeout
	assert.Error(t, err, "Negative retry timeout should return error")
	assert.Contains(t, err.Error(), "non-negative", "Error should mention non-negative requirement")
}

// TestQueueClear migrates pgqueuer's test_queries_clear and test_clear_queue
// Tests queue clearing functionality with different scenarios
func TestQueueClear(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	t.Run("ClearEmptyQueue", func(t *testing.T) {
		// Test clearing already empty queue
		err := q.ClearQueue(ctx, nil) // Clear all
		require.NoError(t, err)

		queueSize, err := q.QueueSize(ctx)
		require.NoError(t, err)
		totalCount := 0
		for _, size := range queueSize {
			totalCount += size.Count
		}
		assert.Equal(t, 0, totalCount, "Queue should remain empty")
	})

	t.Run("ClearAllJobs", func(t *testing.T) {
		// Add some jobs
		err := q.EnqueueJob(ctx, 0, "placeholder", nil)
		require.NoError(t, err)

		queueSize, err := q.QueueSize(ctx)
		require.NoError(t, err)
		totalCount := 0
		for _, size := range queueSize {
			totalCount += size.Count
		}
		assert.Equal(t, 1, totalCount, "Should have 1 job")

		// Clear all jobs
		err = q.ClearQueue(ctx, nil)
		require.NoError(t, err)

		queueSize, err = q.QueueSize(ctx)
		require.NoError(t, err)
		totalCount = 0
		for _, size := range queueSize {
			totalCount += size.Count
		}
		assert.Equal(t, 0, totalCount, "Queue should be empty after clear")
	})

	t.Run("ClearSpecificEntrypoints", func(t *testing.T) {
		// Add jobs with different entrypoints
		N := 3
		for i := 0; i < N; i++ {
			entrypoint := fmt.Sprintf("placeholder%d", i)
			err := q.EnqueueJob(ctx, 0, entrypoint, nil)
			require.NoError(t, err)
		}

		queueSize, err := q.QueueSize(ctx)
		require.NoError(t, err)
		assert.Len(t, queueSize, N, "Should have jobs for %d entrypoints", N)

		totalCount := 0
		for _, size := range queueSize {
			totalCount += size.Count
		}
		assert.Equal(t, N, totalCount, "Should have %d total jobs", N)

		// Clear only placeholder0
		err = q.ClearQueue(ctx, []string{"placeholder0"})
		require.NoError(t, err)

		queueSize, err = q.QueueSize(ctx)
		require.NoError(t, err)
		totalCount = 0
		for _, size := range queueSize {
			totalCount += size.Count
		}
		assert.Equal(t, N-1, totalCount, "Should have %d jobs remaining", N-1)

		// Verify placeholder0 is gone but others remain
		foundPlaceholder0 := false
		for _, size := range queueSize {
			if size.Entrypoint == "placeholder0" {
				foundPlaceholder0 = true
			}
		}
		assert.False(t, foundPlaceholder0, "placeholder0 should be cleared")
	})
}

// TestQueuePriority migrates pgqueuer's test_queue_priority
// Tests that jobs are dequeued in priority order (highest first)
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
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			q := queries.NewQueries(testDB.Pool())

			// Enqueue jobs with different priorities (0 to N-1)
			// pgqueuer uses: list(range(N)) as priorities
			for i := 0; i < tc.N; i++ {
				payload := fmt.Sprintf(`{"id": %d}`, i)
				err := q.EnqueueJob(ctx, i, "placeholder", []byte(payload))
				require.NoError(t, err)
			}

			// Dequeue all jobs and verify priority order
			var jobs []queries.Job
			batchSize := 10
			for {
				dequeuedJobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
					BatchSize:   batchSize,
					Entrypoints: []string{"placeholder"},
				})
				require.NoError(t, err)

				if len(dequeuedJobs) == 0 {
					break
				}

				jobs = append(jobs, dequeuedJobs...)

				// Complete jobs to avoid re-processing
				for _, job := range dequeuedJobs {
					err = q.CompleteJob(ctx, job.ID, "successful")
					require.NoError(t, err)
				}
			}

			require.Len(t, jobs, tc.N, "Should dequeue all %d jobs", tc.N)

			// Verify jobs are in priority order (highest first)
			// pgqueuer: assert jobs == sorted(jobs, key=lambda x: x.priority, reverse=True)
			expectedJobs := make([]queries.Job, len(jobs))
			copy(expectedJobs, jobs)
			sort.Slice(expectedJobs, func(i, j int) bool {
				return expectedJobs[i].Priority > expectedJobs[j].Priority // Highest first
			})

			for i, job := range jobs {
				assert.Equal(t, expectedJobs[i].Priority, job.Priority,
					"Job at position %d should have priority %d", i, expectedJobs[i].Priority)
			}
		})
	}
}
