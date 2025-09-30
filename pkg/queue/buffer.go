package queue

import (
	"context"
	"sync"
	"time"

	"github.com/systemquest/pgqueue4go/pkg/db/generated"
)

// StatisticsBuffer buffers job statistics for batch logging to improve performance
type StatisticsBuffer struct {
	maxSize       int
	timeout       time.Duration
	flushCallback func(ctx context.Context, stats []generated.InsertJobStatisticsParams) error
	buffer        []generated.InsertJobStatisticsParams
	mu            sync.Mutex
	timer         *time.Timer
	alive         bool
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewStatisticsBuffer creates a new statistics buffer
func NewStatisticsBuffer(maxSize int, timeout time.Duration, flushCallback func(ctx context.Context, stats []generated.InsertJobStatisticsParams) error) *StatisticsBuffer {
	ctx, cancel := context.WithCancel(context.Background())

	sb := &StatisticsBuffer{
		maxSize:       maxSize,
		timeout:       timeout,
		flushCallback: flushCallback,
		buffer:        make([]generated.InsertJobStatisticsParams, 0, maxSize),
		alive:         true,
		ctx:           ctx,
		cancel:        cancel,
	}

	// Start the monitoring goroutine
	go sb.monitor()

	return sb
}

// Add adds a statistic entry to the buffer
func (sb *StatisticsBuffer) Add(stat generated.InsertJobStatisticsParams) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if !sb.alive {
		return
	}

	sb.buffer = append(sb.buffer, stat)

	// Reset timer
	if sb.timer != nil {
		sb.timer.Stop()
	}
	sb.timer = time.AfterFunc(sb.timeout, sb.flush)

	// Flush if buffer is full
	if len(sb.buffer) >= sb.maxSize {
		sb.flushLocked()
	}
}

// monitor runs the buffer monitoring loop
func (sb *StatisticsBuffer) monitor() {
	ticker := time.NewTicker(sb.timeout)
	defer ticker.Stop()

	for {
		select {
		case <-sb.ctx.Done():
			// Final flush before shutdown
			sb.mu.Lock()
			if len(sb.buffer) > 0 {
				sb.flushLocked()
			}
			sb.mu.Unlock()
			return

		case <-ticker.C:
			sb.mu.Lock()
			if len(sb.buffer) > 0 {
				sb.flushLocked()
			}
			sb.mu.Unlock()
		}
	}
}

// flush flushes the buffer (called by timer)
func (sb *StatisticsBuffer) flush() {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.flushLocked()
}

// flushLocked flushes the buffer (must be called with lock held)
func (sb *StatisticsBuffer) flushLocked() {
	if len(sb.buffer) == 0 {
		return
	}

	// Make a copy of the buffer
	toFlush := make([]generated.InsertJobStatisticsParams, len(sb.buffer))
	copy(toFlush, sb.buffer)

	// Clear the buffer
	sb.buffer = sb.buffer[:0]

	// Stop the timer
	if sb.timer != nil {
		sb.timer.Stop()
		sb.timer = nil
	}

	// Flush asynchronously to avoid blocking
	go func() {
		if err := sb.flushCallback(sb.ctx, toFlush); err != nil {
			// Log error but continue - we don't want to break the main flow
			// The logger should be passed in, but for now we'll ignore errors
		}
	}()
}

// Stop stops the buffer and flushes remaining data
func (sb *StatisticsBuffer) Stop() {
	sb.mu.Lock()
	sb.alive = false
	sb.mu.Unlock()

	sb.cancel()
}
