package benchmark_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/systemquest/pgqueue/pkg/queue"
	"github.com/systemquest/pgqueue/test/testutil"
)

// BenchmarkProcessJobs benchmarks job processing throughput
func BenchmarkProcessJobs(b *testing.B) {
	testDB := testutil.SetupTestDB(b)
	if testDB == nil {
		b.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var processedCount atomic.Int64

	// Register handler
	err := qm.Entrypoint("bench", func(ctx context.Context, job *queue.Job) error {
		processedCount.Add(1)
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}

	// Enqueue jobs
	for i := 0; i < b.N; i++ {
		err := qm.EnqueueJob(ctx, "bench", []byte(fmt.Sprintf(`{"id": %d}`, i)), 0)
		if err != nil {
			b.Fatal(err)
		}
	}

	// Start processing
	done := make(chan error, 1)
	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 10
		runOpts.DequeueTimeout = 100 * time.Millisecond
		done <- qm.Run(ctx, runOpts)
	}()

	b.ResetTimer()

	// Wait for all jobs to be processed
	for {
		if processedCount.Load() >= int64(b.N) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	b.StopTimer()
	cancel()
	<-done

	// Report throughput
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "jobs/sec")
}

// BenchmarkProcessJobsWithWorkers benchmarks with different worker pool sizes
func BenchmarkProcessJobsWithWorkers(b *testing.B) {
	workerCounts := []int{1, 2, 5, 10, 20}

	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("Workers_%d", workers), func(b *testing.B) {
			testDB := testutil.SetupTestDB(b)
			if testDB == nil {
				b.Skip("PostgreSQL not available")
			}

			qm := queue.NewQueueManager(testDB, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var processedCount atomic.Int64

			// Register handler with minimal work
			err := qm.Entrypoint("bench", func(ctx context.Context, job *queue.Job) error {
				processedCount.Add(1)
				return nil
			})
			if err != nil {
				b.Fatal(err)
			}

			// Enqueue jobs
			jobCount := b.N
			if jobCount < 1000 {
				jobCount = 1000 // Minimum for meaningful benchmark
			}

			for i := 0; i < jobCount; i++ {
				err := qm.EnqueueJob(ctx, "bench", []byte(fmt.Sprintf(`{"id": %d}`, i)), 0)
				if err != nil {
					b.Fatal(err)
				}
			}

			// Start processing
			done := make(chan error, 1)
			go func() {
				runOpts := queue.DefaultRunOptions()
				runOpts.WorkerPoolSize = workers
				runOpts.DequeueTimeout = 100 * time.Millisecond
				done <- qm.Run(ctx, runOpts)
			}()

			b.ResetTimer()

			// Wait for processing
			for {
				if processedCount.Load() >= int64(jobCount) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}

			b.StopTimer()
			cancel()
			<-done

			// Report throughput
			b.ReportMetric(float64(jobCount)/b.Elapsed().Seconds(), "jobs/sec")
		})
	}
}

// BenchmarkEndToEnd benchmarks complete enqueue->process cycle
func BenchmarkEndToEnd(b *testing.B) {
	testDB := testutil.SetupTestDB(b)
	if testDB == nil {
		b.Skip("PostgreSQL not available")
	}

	qm := queue.NewQueueManager(testDB, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var processedCount atomic.Int64

	// Register handler
	err := qm.Entrypoint("bench", func(ctx context.Context, job *queue.Job) error {
		processedCount.Add(1)
		// Simulate some work
		time.Sleep(100 * time.Microsecond)
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}

	// Start processing
	done := make(chan error, 1)
	go func() {
		runOpts := queue.DefaultRunOptions()
		runOpts.WorkerPoolSize = 10
		runOpts.DequeueTimeout = 100 * time.Millisecond
		done <- qm.Run(ctx, runOpts)
	}()

	time.Sleep(100 * time.Millisecond) // Let queue manager start

	b.ResetTimer()

	// Enqueue and process
	for i := 0; i < b.N; i++ {
		err := qm.EnqueueJob(ctx, "bench", []byte(fmt.Sprintf(`{"id": %d}`, i)), 0)
		if err != nil {
			b.Fatal(err)
		}
	}

	// Wait for all jobs to be processed
	for {
		if processedCount.Load() >= int64(b.N) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	b.StopTimer()
	cancel()
	<-done

	// Report latency and throughput
	avgLatency := b.Elapsed() / time.Duration(b.N)
	b.ReportMetric(avgLatency.Seconds()*1000, "ms/op")
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "jobs/sec")
}
