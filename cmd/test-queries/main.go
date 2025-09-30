package main

import (
	"context"
	"fmt"
	"log"

	"github.com/systemquest/pgqueue4go/pkg/config"
	"github.com/systemquest/pgqueue4go/pkg/db"
	"github.com/systemquest/pgqueue4go/pkg/queries"
)

func main() {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Connect to database
	database, err := db.New(ctx, &cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	// Create queries instance
	q := queries.NewQueries(database.Pool())

	fmt.Println("Testing PgQueue4Go Queries API...")

	// Test 1: Queue Size (should be empty initially)
	fmt.Println("\n1. Testing QueueSize:")
	stats, err := q.QueueSize(ctx)
	if err != nil {
		log.Fatalf("Failed to get queue size: %v", err)
	}
	fmt.Printf("Queue size: %d entries\n", len(stats))

	// Test 2: Enqueue a job
	fmt.Println("\n2. Testing EnqueueJob:")
	err = q.EnqueueJob(ctx, 5, "test.handler", []byte("test payload"))
	if err != nil {
		log.Fatalf("Failed to enqueue job: %v", err)
	}
	fmt.Println("Job enqueued successfully")

	// Test 3: Check queue size again
	fmt.Println("\n3. Testing QueueSize after enqueue:")
	stats, err = q.QueueSize(ctx)
	if err != nil {
		log.Fatalf("Failed to get queue size: %v", err)
	}
	for _, stat := range stats {
		fmt.Printf("  Entrypoint: %s, Priority: %d, Count: %d, Status: %s\n",
			stat.Entrypoint, stat.Priority, stat.Count, stat.Status)
	}

	// Test 4: Dequeue jobs
	fmt.Println("\n4. Testing DequeueJobs:")
	jobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
		BatchSize:   1,
		Entrypoints: []string{"test.handler"},
	})
	if err != nil {
		log.Fatalf("Failed to dequeue jobs: %v", err)
	}
	fmt.Printf("Dequeued %d job(s)\n", len(jobs))

	if len(jobs) > 0 {
		job := jobs[0]
		fmt.Printf("  Job ID: %d, Priority: %d, Entrypoint: %s, Status: %s\n",
			job.ID, job.Priority, job.Entrypoint, job.Status)
		fmt.Printf("  Payload: %s\n", string(job.Payload))

		// Test 5: Complete the job
		fmt.Println("\n5. Testing CompleteJob:")
		err = q.CompleteJob(ctx, job.ID, "successful")
		if err != nil {
			log.Fatalf("Failed to complete job: %v", err)
		}
		fmt.Println("Job completed successfully")
	}

	// Test 6: Check queue size after completion
	fmt.Println("\n6. Testing QueueSize after completion:")
	stats, err = q.QueueSize(ctx)
	if err != nil {
		log.Fatalf("Failed to get queue size: %v", err)
	}
	fmt.Printf("Queue size: %d entries\n", len(stats))

	// Test 7: Check log statistics
	fmt.Println("\n7. Testing LogStatistics:")
	logStats, err := q.LogStatistics(ctx, 10)
	if err != nil {
		log.Fatalf("Failed to get log statistics: %v", err)
	}
	fmt.Printf("Log statistics: %d entries\n", len(logStats))
	for _, stat := range logStats {
		fmt.Printf("  Entrypoint: %s, Status: %s, Count: %d, TimeInQueue: %v\n",
			stat.Entrypoint, stat.Status, stat.Count, stat.TimeInQueue)
	}

	fmt.Println("\n✅ All tests passed! PgQueue4Go Queries API is working correctly.")
}
