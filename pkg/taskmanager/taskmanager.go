package taskmanager

import (
	"context"
	"sync"
)

// TaskManager manages a collection of goroutines, similar to pgqueuer's TaskManager
// This provides functionality equivalent to Python's asyncio TaskManager
type TaskManager struct {
	tasks   []func() error
	mu      sync.Mutex
	wg      sync.WaitGroup
	results []error
}

// NewTaskManager creates a new task manager instance
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks:   make([]func() error, 0),
		results: make([]error, 0),
	}
}

// Add adds a task (function) to the manager - similar to pgqueuer's tm.add(asyncio.create_task())
func (tm *TaskManager) Add(task func() error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.tasks = append(tm.tasks, task)
}

// Count returns the number of managed tasks - similar to pgqueuer's len(tm.tasks)
func (tm *TaskManager) Count() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return len(tm.tasks)
}

// Run executes all tasks concurrently and waits for completion
// Similar to pgqueuer's await asyncio.gather(*tm.tasks)
func (tm *TaskManager) Run() []error {
	tm.mu.Lock()
	tasks := make([]func() error, len(tm.tasks))
	copy(tasks, tm.tasks)
	tm.results = make([]error, len(tasks))
	tm.mu.Unlock()

	// Start all tasks as goroutines
	for i, task := range tasks {
		tm.wg.Add(1)
		go func(index int, t func() error) {
			defer tm.wg.Done()
			tm.results[index] = t()
		}(i, task)
	}

	// Wait for all tasks to complete
	tm.wg.Wait()

	// Clear tasks after completion - similar to pgqueuer's behavior
	tm.mu.Lock()
	tm.tasks = tm.tasks[:0]
	tm.mu.Unlock()

	return tm.results
}

// Clear removes all tasks from the manager
func (tm *TaskManager) Clear() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.tasks = tm.tasks[:0]
}

// ContextManager creates a context-aware task manager similar to pgqueuer's async with TaskManager()
type ContextManager struct {
	tm     *TaskManager
	cancel context.CancelFunc
}

// NewContextManager creates a context-aware task manager
func NewContextManager(ctx context.Context) (*ContextManager, context.Context) {
	ctxWithCancel, cancel := context.WithCancel(ctx)
	return &ContextManager{
		tm:     NewTaskManager(),
		cancel: cancel,
	}, ctxWithCancel
}

// Add adds a task to the context-aware manager
func (cm *ContextManager) Add(task func() error) {
	cm.tm.Add(task)
}

// Count returns the number of managed tasks
func (cm *ContextManager) Count() int {
	return cm.tm.Count()
}

// Close executes all tasks and cleans up - similar to pgqueuer's __aexit__
func (cm *ContextManager) Close() []error {
	defer cm.cancel()
	results := cm.tm.Run()
	return results
}

// GetTaskManager returns the underlying task manager
func (cm *ContextManager) GetTaskManager() *TaskManager {
	return cm.tm
}
