package queue

import (
	"context"
	"sync"
	"time"
)

// StatisticsBuffer is now a no-op since statistics are handled automatically
// by CompleteJob in the new Queries API. This maintains API compatibility.
type StatisticsBuffer struct {
	alive  bool
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

// NewStatisticsBuffer creates a new statistics buffer (now a no-op)
func NewStatisticsBuffer(maxSize int, timeout time.Duration, flushCallback func(ctx context.Context, stats []interface{}) error) *StatisticsBuffer {
	ctx, cancel := context.WithCancel(context.Background())

	sb := &StatisticsBuffer{
		alive:  true,
		ctx:    ctx,
		cancel: cancel,
	}

	return sb
}

// Add adds a statistic entry to the buffer (now a no-op since statistics are automatic)
func (sb *StatisticsBuffer) Add(stat interface{}) {
	// No-op: Statistics are now handled automatically by CompleteJob
}

// Stop stops the buffer
func (sb *StatisticsBuffer) Stop() {
	sb.mu.Lock()
	sb.alive = false
	sb.mu.Unlock()

	sb.cancel()
}
