package benchmark_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/systemquest/pgqueue/pkg/queue"
	"github.com/systemquest/pgqueue/test/testutil"
)

// BenchmarkEnqueueSingle benchmarks single job enqueue
func BenchmarkEnqueueSingle(b *testing.B) {
	testDB := testutil.SetupTestDB(b)
	if testDB == nil {
		b.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := qm.EnqueueJob(ctx, "bench", []byte(fmt.Sprintf(`{"id": %d}`, i)), 0)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()

	// Report operations per second
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

// BenchmarkEnqueueBatch benchmarks batch job enqueue
func BenchmarkEnqueueBatch(b *testing.B) {
	batchSizes := []int{10, 50, 100, 500, 1000}

	for _, size := range batchSizes {
		b.Run(fmt.Sprintf("BatchSize_%d", size), func(b *testing.B) {
			testDB := testutil.SetupTestDB(b)
			if testDB == nil {
				b.Skip("PostgreSQL not available")
			}

			qm := queue.NewQueueManager(testDB, nil)
			ctx := context.Background()

			// Prepare batch
			jobs := make([]queue.EnqueueJobRequest, size)
			for i := 0; i < size; i++ {
				jobs[i] = queue.EnqueueJobRequest{
					Entrypoint: "bench",
					Payload:    []byte(fmt.Sprintf(`{"id": %d}`, i)),
					Priority:   0,
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				err := qm.EnqueueJobs(ctx, jobs)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()

			// Report operations per second (jobs per second)
			totalJobs := float64(b.N * size)
			b.ReportMetric(totalJobs/b.Elapsed().Seconds(), "jobs/sec")
		})
	}
}

// BenchmarkEnqueuePriority benchmarks enqueue with different priorities
func BenchmarkEnqueuePriority(b *testing.B) {
	testDB := testutil.SetupTestDB(b)
	if testDB == nil {
		b.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)
	ctx := context.Background()

	priorities := []int32{0, 5, 10}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		priority := priorities[i%len(priorities)]
		err := qm.EnqueueJob(ctx, "bench", []byte(fmt.Sprintf(`{"id": %d}`, i)), priority)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

// BenchmarkEnqueueConcurrent benchmarks concurrent enqueue operations
func BenchmarkEnqueueConcurrent(b *testing.B) {
	testDB := testutil.SetupTestDB(b)
	if testDB == nil {
		b.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			err := qm.EnqueueJob(ctx, "bench", []byte(fmt.Sprintf(`{"id": %d}`, i)), 0)
			if err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
	b.StopTimer()

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}
