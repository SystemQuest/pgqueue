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

	// Test with prefix
	prefix := "test_"
	fmt.Printf("Testing PgQueue4Go with prefix '%s'...\n", prefix)

	// Create queries instance with prefix
	q := queries.NewQueriesWithPrefix(database.Pool(), prefix)

	// Install schema with prefix
	fmt.Println("\n1. Installing schema with prefix...")
	err = q.Install(ctx)
	if err != nil {
		log.Fatalf("Failed to install schema: %v", err)
	}
	fmt.Println("Schema installed successfully")

	// Test operations with prefixed tables
	fmt.Println("\n2. Testing operations with prefixed tables...")

	// Enqueue a job
	err = q.EnqueueJob(ctx, 10, "prefixed.handler", []byte("prefixed payload"))
	if err != nil {
		log.Fatalf("Failed to enqueue job: %v", err)
	}
	fmt.Println("Job enqueued to prefixed table")

	// Check queue size
	stats, err := q.QueueSize(ctx)
	if err != nil {
		log.Fatalf("Failed to get queue size: %v", err)
	}
	fmt.Printf("Prefixed queue size: %d entries\n", len(stats))
	for _, stat := range stats {
		fmt.Printf("  Entrypoint: %s, Priority: %d, Count: %d, Status: %s\n",
			stat.Entrypoint, stat.Priority, stat.Count, stat.Status)
	}

	// Dequeue and complete job
	jobs, err := q.DequeueJobs(ctx, queries.DequeueJobsParams{
		BatchSize:   1,
		Entrypoints: []string{"prefixed.handler"},
	})
	if err != nil {
		log.Fatalf("Failed to dequeue jobs: %v", err)
	}

	if len(jobs) > 0 {
		job := jobs[0]
		fmt.Printf("Dequeued job from prefixed table: ID=%d, Payload=%s\n", job.ID, string(job.Payload))

		err = q.CompleteJob(ctx, job.ID, "successful")
		if err != nil {
			log.Fatalf("Failed to complete job: %v", err)
		}
		fmt.Println("Job completed in prefixed tables")
	}

	// Clean up - uninstall schema
	fmt.Println("\n3. Cleaning up prefixed schema...")
	err = q.Uninstall(ctx)
	if err != nil {
		log.Fatalf("Failed to uninstall schema: %v", err)
	}
	fmt.Println("Prefixed schema uninstalled successfully")

	fmt.Println("\n✅ Prefix functionality test passed!")
}
