package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/systemquest/pgqueue/pkg/config"
	"github.com/systemquest/pgqueue/pkg/db"
	"github.com/systemquest/pgqueue/pkg/queue"
)

// BenchmarkConfig holds benchmark configuration
type BenchmarkConfig struct {
	Timer             time.Duration
	DequeueWorkers    int
	DequeueBatchSize  int
	EnqueueWorkers    int
	EnqueueBatchSize  int
	DatabaseURL       string
	ShowQueueSize     bool
	QueueSizeInterval time.Duration
	ClearDB           bool // Whether to clear database before benchmark
}

// Timer provides elapsed time tracking
type Timer struct {
	start time.Time
}

// NewTimer creates a new timer
func NewTimer() *Timer {
	return &Timer{start: time.Now()}
}

// Elapsed returns the elapsed duration
func (t *Timer) Elapsed() time.Duration {
	return time.Since(t.start)
}

// Consumer runs a queue consumer and returns jobs/second rate
func Consumer(ctx context.Context, database *db.DB, batchSize int, jobCount *atomic.Int64) (float64, error) {
	qm := queue.NewQueueManager(database, slog.Default())

	// Register entrypoints
	err := qm.Entrypoint("asyncfetch", func(ctx context.Context, job *queue.Job) error {
		jobCount.Add(1)

		// Stop signal (empty payload)
		if len(job.Payload) == 0 {
			qm.Stop()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	err = qm.Entrypoint("syncfetch", func(ctx context.Context, job *queue.Job) error {
		jobCount.Add(1)

		// Stop signal (empty payload)
		if len(job.Payload) == 0 {
			qm.Stop()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	timer := NewTimer()

	// Run queue manager
	opts := &queue.RunOptions{
		DequeueTimeout: 500 * time.Millisecond,
		BatchSize:      int32(batchSize),
		WorkerPoolSize: 3,
	}

	// Run in background
	go func() {
		if err := qm.Run(ctx, opts); err != nil && err != context.Canceled {
			slog.Error("Queue manager error", "error", err)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()

	elapsed := timer.Elapsed()
	count := jobCount.Load()

	// Gracefully shutdown queue manager before closing database
	// This ensures statistics buffer is flushed while connection pool is still open
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := qm.Shutdown(shutdownCtx); err != nil {
		slog.Warn("Queue manager shutdown error", "error", err)
	}

	if elapsed.Seconds() == 0 {
		return 0, nil
	}

	return float64(count) / elapsed.Seconds(), nil
}

// Producer continuously enqueues jobs
func Producer(ctx context.Context, database *db.DB, batchSize int, counter *atomic.Int64) error {
	qm := queue.NewQueueManager(database, nil)

	entrypoints := []string{"syncfetch", "asyncfetch"}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Create batch of jobs
			jobs := make([]queue.EnqueueJobRequest, batchSize)
			for i := 0; i < batchSize; i++ {
				// Random entrypoint
				entrypoint := entrypoints[rand.Intn(len(entrypoints))]

				// Generate payload
				payload := []byte(fmt.Sprintf("%d", counter.Add(1)))

				jobs[i] = queue.EnqueueJobRequest{
					Entrypoint: entrypoint,
					Payload:    payload,
					Priority:   0,
				}
			}

			// Enqueue batch
			if err := qm.EnqueueJobs(ctx, jobs); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				slog.Error("Failed to enqueue jobs", "error", err)
			}
		}
	}
}

// MonitorQueueSize periodically displays queue size
func MonitorQueueSize(ctx context.Context, database *db.DB, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats, err := database.Queries().QueueSize(ctx)
			if err != nil {
				slog.Error("Failed to get queue stats", "error", err)
				continue
			}

			var total int64
			for _, stat := range stats {
				total += int64(stat.Count)
			}

			fmt.Printf("Queue size: %d\n", total)
		}
	}
}

// ClearDatabase clears queue and log tables
func ClearDatabase(ctx context.Context, database *db.DB) error {
	// Clear queue
	if _, err := database.Pool().Exec(ctx, "TRUNCATE TABLE pgqueue_jobs"); err != nil {
		return fmt.Errorf("failed to clear queue: %w", err)
	}

	// Clear logs
	if _, err := database.Pool().Exec(ctx, "TRUNCATE TABLE pgqueue_statistics"); err != nil {
		return fmt.Errorf("failed to clear logs: %w", err)
	}

	return nil
}

func main() {
	// Parse command line flags
	cfg := &BenchmarkConfig{}

	var timerSeconds float64
	flag.Float64Var(&timerSeconds, "t", 15, "Run the benchmark for specified seconds")
	flag.Float64Var(&timerSeconds, "timer", 15, "Run the benchmark for specified seconds")

	flag.IntVar(&cfg.DequeueWorkers, "dq", 2, "Number of concurrent dequeue workers")
	flag.IntVar(&cfg.DequeueWorkers, "dequeue", 2, "Number of concurrent dequeue workers")

	flag.IntVar(&cfg.DequeueBatchSize, "dqbs", 10, "Batch size for dequeue workers")
	flag.IntVar(&cfg.DequeueBatchSize, "dequeue-batch-size", 10, "Batch size for dequeue workers")

	flag.IntVar(&cfg.EnqueueWorkers, "eq", 1, "Number of concurrent enqueue workers")
	flag.IntVar(&cfg.EnqueueWorkers, "enqueue", 1, "Number of concurrent enqueue workers")

	flag.IntVar(&cfg.EnqueueBatchSize, "eqbs", 20, "Batch size for enqueue workers")
	flag.IntVar(&cfg.EnqueueBatchSize, "enqueue-batch-size", 20, "Batch size for enqueue workers")

	flag.StringVar(&cfg.DatabaseURL, "db", "", "Database connection URL")
	flag.BoolVar(&cfg.ShowQueueSize, "show-queue-size", true, "Show queue size periodically")
	flag.BoolVar(&cfg.ClearDB, "clear-db", true, "Clear database before benchmark (default: true)")

	flag.Parse()

	cfg.Timer = time.Duration(timerSeconds * float64(time.Second))
	cfg.QueueSizeInterval = cfg.Timer / 10

	// Get database URL from env if not provided
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = os.Getenv("DATABASE_URL")
		if cfg.DatabaseURL == "" {
			cfg.DatabaseURL = "postgres://postgres:password@localhost:5432/pgqueue?sslmode=disable"
		}
	}

	// Display settings
	fmt.Printf(`Settings:
Timer:                  %.2f seconds
Dequeue:                %d
Dequeue Batch Size:     %d
Enqueue:                %d
Enqueue Batch Size:     %d

`, cfg.Timer.Seconds(), cfg.DequeueWorkers, cfg.DequeueBatchSize,
		cfg.EnqueueWorkers, cfg.EnqueueBatchSize)

	// Setup database connection
	ctx := context.Background()
	dbCfg := &config.DatabaseConfig{
		URL:            cfg.DatabaseURL,
		MaxConnections: 25,
		MaxIdleTime:    5 * time.Minute,
		MaxLifetime:    1 * time.Hour,
		ConnectTimeout: 10 * time.Second,
	}

	utilDB, err := db.New(ctx, dbCfg)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer utilDB.Close()

	// Clear database if requested
	if cfg.ClearDB {
		fmt.Println("Clearing database...")
		if err := ClearDatabase(ctx, utilDB); err != nil {
			slog.Error("Failed to clear database", "error", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("Skipping database cleanup (using existing data)...")
	}

	// Create context with timeout
	benchCtx, cancel := context.WithTimeout(ctx, cfg.Timer)
	defer cancel()

	var wg sync.WaitGroup

	// Start enqueue workers
	enqueueCounter := &atomic.Int64{}
	for i := 0; i < cfg.EnqueueWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Create dedicated connection
			workerDB, err := db.New(ctx, dbCfg)
			if err != nil {
				slog.Error("Failed to create worker DB", "worker_id", workerID, "error", err)
				return
			}
			defer workerDB.Close()

			if err := Producer(benchCtx, workerDB, cfg.EnqueueBatchSize, enqueueCounter); err != nil {
				if err != context.Canceled && err != context.DeadlineExceeded {
					slog.Error("Producer error", "worker_id", workerID, "error", err)
				}
			}
		}(i)
	}

	// Start dequeue workers
	jobCounts := make([]*atomic.Int64, cfg.DequeueWorkers)
	jobsPerSecond := make([]float64, cfg.DequeueWorkers)

	for i := 0; i < cfg.DequeueWorkers; i++ {
		jobCounts[i] = &atomic.Int64{}

		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Create dedicated connection
			workerDB, err := db.New(ctx, dbCfg)
			if err != nil {
				slog.Error("Failed to create worker DB", "worker_id", workerID, "error", err)
				return
			}
			defer workerDB.Close()

			jps, err := Consumer(benchCtx, workerDB, cfg.DequeueBatchSize, jobCounts[workerID])
			if err != nil {
				slog.Error("Consumer error", "worker_id", workerID, "error", err)
				return
			}

			jobsPerSecond[workerID] = jps
		}(i)
	}

	// Start queue size monitor
	if cfg.ShowQueueSize {
		wg.Add(1)
		go func() {
			defer wg.Done()
			MonitorQueueSize(benchCtx, utilDB, cfg.QueueSizeInterval)
		}()
	}

	// Wait for completion
	wg.Wait()

	// Calculate total jobs per second
	var totalJPS float64
	var totalJobs int64
	for i, jps := range jobsPerSecond {
		totalJPS += jps
		totalJobs += jobCounts[i].Load()
	}

	fmt.Printf("\n=== Benchmark Results ===\n")
	fmt.Printf("Total Jobs Processed:   %d\n", totalJobs)
	fmt.Printf("Jobs per Second:        %.2fk\n", totalJPS/1000)
	fmt.Printf("Average per Worker:     %.2f jobs/sec\n", totalJPS/float64(cfg.DequeueWorkers))
	fmt.Printf("\nPer-Worker Stats:\n")
	for i, jps := range jobsPerSecond {
		fmt.Printf("  Worker %d: %.2f jobs/sec (%d jobs)\n", i, jps, jobCounts[i].Load())
	}
}
