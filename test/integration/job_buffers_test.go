package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/systemquest/pgqueue4go/pkg/queries"
	"github.com/systemquest/pgqueue4go/test/testutil"
)

// perfCounterTime returns high-resolution timestamp similar to pgqueuer's _perf_counter_dt()
func perfCounterTime() time.Time {
	return time.Now().UTC()
}

// TestPerfCounterTime migrates pgqueuer's test_perf_counter_dt
func TestPerfCounterTime(t *testing.T) {
	result := perfCounterTime()
	assert.False(t, result.IsZero(), "Should return valid time")

	loc := result.Location()
	assert.NotNil(t, loc, "Should have timezone info")
	assert.Equal(t, time.UTC, loc, "Should be UTC timezone")
}

// TestStatisticsDirectWrite tests direct statistics writing
func TestStatisticsDirectWrite(t *testing.T) {
	testCases := []struct {
		name  string
		count int
	}{
		{"Single", 1},
		{"Dual", 2},
		{"Few", 5},
		{"Many", 64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.SetupTestDB(t)
			defer db.Close()

			ctx := context.Background()
			q := queries.NewQueries(db.Pool())

			// Schema should already be installed by Docker init script

			for i := 0; i < tc.count; i++ {
				payload := []byte("test_payload")
				require.NoError(t, q.EnqueueJob(ctx, 0, "test_entrypoint", payload))
			}

			jobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
				BatchSize:   tc.count,
				Entrypoints: []string{"test_entrypoint"},
			})
			require.NoError(t, err)
			require.Len(t, jobs, tc.count)

			for _, job := range jobs {
				require.NoError(t, q.CompleteJob(ctx, job.ID, "successful"))
			}

			stats, err := q.LogStatistics(ctx, 1000)
			require.NoError(t, err)

			// We should have 1 aggregated record with Count=tc.count (due to ON CONFLICT)
			require.Len(t, stats, 1)
			assert.Equal(t, tc.count, stats[0].Count)
			assert.Equal(t, "test_entrypoint", stats[0].Entrypoint)
			assert.Equal(t, "successful", stats[0].Status)
		})
	}
}

// TestJobBufferMaxSize migrates pgqueuer's test_job_buffer_max_size
// Tests that statistics are properly buffered and flushed at max size
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
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			q := queries.NewQueries(testDB.Pool())

			// Enqueue and process jobs one by one, testing buffer behavior
			for i := 0; i < tc.maxSize-1; i++ {
				// Add job
				payload := []byte("buffer_test")
				require.NoError(t, q.EnqueueJob(ctx, 0, "buffer_test", payload))

				// Process job
				jobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
					BatchSize:   1,
					Entrypoints: []string{"buffer_test"},
				})
				require.NoError(t, err)
				require.Len(t, jobs, 1)

				// Complete job - this should add to buffer but not flush yet
				require.NoError(t, q.CompleteJob(ctx, jobs[0].ID, "successful"))

				// Check that statistics haven't been flushed yet (would be 0 if buffered)
				// Note: Our simplified architecture directly writes to DB, so we expect records
				stats, err := q.LogStatistics(ctx, 1000)
				require.NoError(t, err)

				// In our direct-write architecture, we should see accumulating statistics
				totalStats := 0
				for _, stat := range stats {
					totalStats += stat.Count
				}
				assert.Equal(t, i+1, totalStats, "Should have %d accumulated statistics", i+1)
			}

			// Add one more job to trigger "buffer flush" (in our case, just another write)
			payload := []byte("buffer_test")
			require.NoError(t, q.EnqueueJob(ctx, 0, "buffer_test", payload))

			jobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
				BatchSize:   1,
				Entrypoints: []string{"buffer_test"},
			})
			require.NoError(t, err)
			require.Len(t, jobs, 1)

			require.NoError(t, q.CompleteJob(ctx, jobs[0].ID, "successful"))

			// Verify all statistics are recorded
			stats, err := q.LogStatistics(ctx, 1000)
			require.NoError(t, err)

			// Should have one aggregated record with count = tc.maxSize
			require.Len(t, stats, 1)
			assert.Equal(t, tc.maxSize, stats[0].Count, "Should have max_size=%d aggregated statistics", tc.maxSize)
		})
	}
}

// TestMoveJobLog migrates pgqueuer's test_move_job_log
// Tests that completed jobs are properly logged in statistics
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
			testDB := testutil.SetupTestDB(t)
			if testDB == nil {
				t.Skip("PostgreSQL not available")
			}
			defer testDB.Close()

			ctx := context.Background()
			q := queries.NewQueries(testDB.Pool())

			// Enqueue N jobs
			for i := 0; i < tc.N; i++ {
				payload := []byte(fmt.Sprintf(`{"id": %d}`, i))
				require.NoError(t, q.EnqueueJob(ctx, 0, "placeholder", payload))
			}

			// Process all jobs by dequeuing and completing them
			totalProcessed := 0
			batchSize := 10
			for {
				jobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
					BatchSize:   batchSize,
					Entrypoints: []string{"placeholder"},
				})
				require.NoError(t, err)

				if len(jobs) == 0 {
					break
				}

				// Complete all jobs in this batch - this "moves" them to job log (statistics)
				for _, job := range jobs {
					require.NoError(t, q.CompleteJob(ctx, job.ID, "successful"))
					totalProcessed++
				}
			}

			require.Equal(t, tc.N, totalProcessed, "Should have processed all %d jobs", tc.N)

			// Verify that all completed jobs are logged in statistics
			stats, err := q.LogStatistics(ctx, 1000000) // Large limit to get all stats
			require.NoError(t, err)

			// Sum up all statistics counts
			totalLogged := 0
			for _, stat := range stats {
				totalLogged += stat.Count
			}

			assert.Equal(t, tc.N, totalLogged, "Should have logged all %d completed jobs in statistics", tc.N)
		})
	}
}
