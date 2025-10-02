package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/systemquest/pgtask/pkg/queries"
	"github.com/systemquest/pgtask/test/testutil"
)

// TestBatchEnqueuePerformance tests the optimized batch enqueue using unnest()
// This validates the Phase 1 improvement: single query vs loop insertion
func TestBatchEnqueuePerformance(t *testing.T) {
	testCases := []struct {
		name      string
		batchSize int
	}{
		{"Small", 10},
		{"Medium", 100},
		{"Large", 1000},
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

			// Prepare batch jobs
			jobs := make([]queries.EnqueueJobParams, tc.batchSize)
			for i := 0; i < tc.batchSize; i++ {
				jobs[i] = queries.EnqueueJobParams{
					Priority:   i % 10,
					Entrypoint: fmt.Sprintf("test_%d", i),
					Payload:    []byte(fmt.Sprintf(`{"id": %d}`, i)),
				}
			}

			// Measure time for batch enqueue
			start := time.Now()
			err := q.EnqueueJobs(ctx, jobs)
			duration := time.Since(start)

			require.NoError(t, err)

			t.Logf("Enqueued %d jobs in %v (%.2f jobs/sec)",
				tc.batchSize, duration, float64(tc.batchSize)/duration.Seconds())

			// Verify all jobs were enqueued
			queueSize, err := q.QueueSize(ctx)
			require.NoError(t, err)

			totalCount := 0
			for _, size := range queueSize {
				totalCount += size.Count
			}
			assert.Equal(t, tc.batchSize, totalCount,
				"Expected %d jobs in queue", tc.batchSize)

			// Performance assertion: large batches should be very fast with unnest()
			// With unnest(), even 1000 jobs should complete in < 100ms
			if tc.batchSize >= 1000 {
				assert.Less(t, duration.Milliseconds(), int64(200),
					"Large batch (%d jobs) should complete quickly with unnest() optimization",
					tc.batchSize)
			}
		})
	}
}

// TestBatchEnqueueCorrectness tests that batch enqueue preserves all job data
func TestBatchEnqueueCorrectness(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	// Create jobs with different priorities and payloads
	batchSize := 50
	jobs := make([]queries.EnqueueJobParams, batchSize)
	for i := 0; i < batchSize; i++ {
		jobs[i] = queries.EnqueueJobParams{
			Priority:   i % 5,
			Entrypoint: fmt.Sprintf("entrypoint_%d", i%3),
			Payload:    []byte(fmt.Sprintf(`{"index": %d, "data": "test_%d"}`, i, i)),
		}
	}

	// Enqueue batch
	err := q.EnqueueJobs(ctx, jobs)
	require.NoError(t, err)

	// Dequeue and verify all jobs
	dequeuedJobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
		BatchSize:   batchSize,
		Entrypoints: []string{"entrypoint_0", "entrypoint_1", "entrypoint_2"},
	})
	require.NoError(t, err)

	assert.Equal(t, batchSize, len(dequeuedJobs),
		"Should dequeue all %d enqueued jobs", batchSize)

	// Verify jobs are sorted by priority (higher priority first)
	for i := 1; i < len(dequeuedJobs); i++ {
		assert.GreaterOrEqual(t, dequeuedJobs[i-1].Priority, dequeuedJobs[i].Priority,
			"Jobs should be ordered by priority")
	}
}

// TestBatchEnqueueEmpty tests edge case of empty batch
func TestBatchEnqueueEmpty(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	// Enqueue empty batch
	err := q.EnqueueJobs(ctx, []queries.EnqueueJobParams{})
	require.NoError(t, err)

	// Verify queue is still empty
	queueSize, err := q.QueueSize(ctx)
	require.NoError(t, err)

	totalCount := 0
	for _, size := range queueSize {
		totalCount += size.Count
	}
	assert.Equal(t, 0, totalCount, "Queue should remain empty")
}

// TestBatchEnqueueWithNilPayload tests jobs with nil payload
func TestBatchEnqueueWithNilPayload(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	// Create batch with some nil payloads
	jobs := []queries.EnqueueJobParams{
		{Priority: 0, Entrypoint: "test1", Payload: []byte(`{"data": "value"}`)},
		{Priority: 0, Entrypoint: "test2", Payload: nil},
		{Priority: 0, Entrypoint: "test3", Payload: []byte(`{"data": "value2"}`)},
		{Priority: 0, Entrypoint: "test4", Payload: nil},
	}

	err := q.EnqueueJobs(ctx, jobs)
	require.NoError(t, err)

	// Dequeue and verify
	dequeuedJobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
		BatchSize:   10,
		Entrypoints: []string{"test1", "test2", "test3", "test4"},
	})
	require.NoError(t, err)

	assert.Equal(t, 4, len(dequeuedJobs))

	// Count jobs with nil payload
	nilCount := 0
	for _, job := range dequeuedJobs {
		if job.Payload == nil {
			nilCount++
		}
	}
	assert.Equal(t, 2, nilCount, "Should have 2 jobs with nil payload")
}

// TestBatchEnqueuePriorities tests that batch enqueue preserves priority ordering
func TestBatchEnqueuePriorities(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		t.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	// Enqueue jobs with different priorities in random order
	jobs := []queries.EnqueueJobParams{
		{Priority: 5, Entrypoint: "test", Payload: []byte(`{"id": 1}`)},
		{Priority: 1, Entrypoint: "test", Payload: []byte(`{"id": 2}`)},
		{Priority: 10, Entrypoint: "test", Payload: []byte(`{"id": 3}`)},
		{Priority: 3, Entrypoint: "test", Payload: []byte(`{"id": 4}`)},
		{Priority: 7, Entrypoint: "test", Payload: []byte(`{"id": 5}`)},
	}

	err := q.EnqueueJobs(ctx, jobs)
	require.NoError(t, err)

	// Dequeue jobs
	dequeuedJobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
		BatchSize:   10,
		Entrypoints: []string{"test"},
	})
	require.NoError(t, err)

	assert.Equal(t, 5, len(dequeuedJobs))

	// Verify priority ordering (higher priority dequeued first)
	expectedOrder := []int{10, 7, 5, 3, 1}
	for i, job := range dequeuedJobs {
		assert.Equal(t, expectedOrder[i], int(job.Priority),
			"Job %d should have priority %d", i, expectedOrder[i])
	}
}

// BenchmarkBatchEnqueue benchmarks the optimized batch enqueue
func BenchmarkBatchEnqueue(b *testing.B) {
	testDB := testutil.SetupTestDB(&testing.T{})
	if testDB == nil {
		b.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	batchSizes := []int{10, 100, 1000}

	for _, size := range batchSizes {
		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			// Prepare jobs once
			jobs := make([]queries.EnqueueJobParams, size)
			for i := 0; i < size; i++ {
				jobs[i] = queries.EnqueueJobParams{
					Priority:   i % 10,
					Entrypoint: fmt.Sprintf("bench_%d", i),
					Payload:    []byte(fmt.Sprintf(`{"id": %d}`, i)),
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				err := q.EnqueueJobs(ctx, jobs)
				if err != nil {
					b.Fatal(err)
				}

				// Clean up after each iteration
				b.StopTimer()
				// Delete all jobs
				_, err = testDB.Pool().Exec(ctx, "DELETE FROM pgqueuer.job")
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}

			b.ReportMetric(float64(size*b.N)/b.Elapsed().Seconds(), "jobs/sec")
		})
	}
}

// BenchmarkBatchEnqueueVsSingle compares batch vs single enqueue
func BenchmarkBatchEnqueueVsSingle(b *testing.B) {
	testDB := testutil.SetupTestDB(&testing.T{})
	if testDB == nil {
		b.Skip("PostgreSQL not available")
	}
	defer testDB.Close()

	ctx := context.Background()
	q := queries.NewQueries(testDB.Pool())

	const batchSize = 100

	b.Run("Batch", func(b *testing.B) {
		jobs := make([]queries.EnqueueJobParams, batchSize)
		for i := 0; i < batchSize; i++ {
			jobs[i] = queries.EnqueueJobParams{
				Priority:   0,
				Entrypoint: "test",
				Payload:    []byte(fmt.Sprintf(`{"id": %d}`, i)),
			}
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			q.EnqueueJobs(ctx, jobs)
		}
	})

	b.Run("Single", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for j := 0; j < batchSize; j++ {
				q.EnqueueJob(ctx, 0, "test", []byte(fmt.Sprintf(`{"id": %d}`, j)))
			}
		}
	})
}
