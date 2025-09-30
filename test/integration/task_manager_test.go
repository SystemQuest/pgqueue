package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/systemquest/pgqueue4go/pkg/taskmanager"
)

// TestTaskManager migrates pgqueuer's test_task_manager
// Tests basic task manager functionality with parametrized N values (1, 2, 3, 5, 64)
func TestTaskManager(t *testing.T) {
	testCases := []struct {
		name string
		N    int
	}{
		{"Single", 1},
		{"Dual", 2},
		{"Triple", 3},
		{"Five", 5},
		{"Many", 64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a channel to simulate pgqueuer's asyncio.Future
			done := make(chan struct{})

			tm := taskmanager.NewTaskManager()
			assert.Equal(t, 0, tm.Count(), "Initial task count should be 0")

			// Create waiter function similar to pgqueuer's async def waiter(future)
			waiter := func() error {
				<-done // Wait for signal, similar to await future
				return nil
			}

			// Add N tasks - similar to pgqueuer's loop adding tasks
			for i := 0; i < tc.N; i++ {
				tm.Add(waiter)
			}

			assert.Equal(t, tc.N, tm.Count(), "Task count should equal N after adding")

			// Start tasks in background and signal completion
			go func() {
				time.Sleep(10 * time.Millisecond) // Small delay to ensure tasks are waiting
				close(done)                       // Signal completion, similar to future.set_result(None)
			}()

			// Execute all tasks - similar to pgqueuer's await asyncio.gather(*tm.tasks)
			results := tm.Run()

			// Verify all tasks completed successfully
			assert.Len(t, results, tc.N, "Should have results for all tasks")
			for i, err := range results {
				assert.NoError(t, err, "Task %d should complete without error", i)
			}

			// Verify tasks are cleared after completion - similar to pgqueuer's assert len(tm.tasks) == 0
			assert.Equal(t, 0, tm.Count(), "Task count should be 0 after completion")
		})
	}
}

// TestTaskManagerContextManager migrates pgqueuer's test_task_manager_ctx_mngr
// Tests context manager functionality with parametrized N values (1, 2, 3, 5, 64)
func TestTaskManagerContextManager(t *testing.T) {
	testCases := []struct {
		name string
		N    int
	}{
		{"Single", 1},
		{"Dual", 2},
		{"Triple", 3},
		{"Five", 5},
		{"Many", 64},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Create waiter function similar to pgqueuer's async def waiter(future)
			done := make(chan struct{})
			waiter := func() error {
				<-done // Wait for signal, similar to await future
				return nil
			}

			// Create context manager - similar to pgqueuer's async with TaskManager() as tm:
			cm, _ := taskmanager.NewContextManager(ctx)

			assert.Equal(t, 0, cm.Count(), "Initial task count should be 0")

			// Add N tasks - similar to pgqueuer's loop adding tasks
			for i := 0; i < tc.N; i++ {
				cm.Add(waiter)
			}

			assert.Equal(t, tc.N, cm.Count(), "Task count should equal N after adding")

			// Signal completion in background
			go func() {
				time.Sleep(10 * time.Millisecond) // Small delay to ensure tasks are waiting
				close(done)                       // Signal completion, similar to future.set_result(None)
			}()

			// Close context manager - similar to exiting async with block
			results := cm.Close()

			// Verify all tasks completed successfully
			assert.Len(t, results, tc.N, "Should have results for all tasks")
			for i, err := range results {
				assert.NoError(t, err, "Task %d should complete without error", i)
			}

			// Verify tasks are cleared after context exit - similar to pgqueuer's assert len(tm.tasks) == 0
			assert.Equal(t, 0, cm.Count(), "Task count should be 0 after context exit")
		})
	}
}
