package queue

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// JobCompletion represents a completed job with its status
type JobCompletion struct {
	Job    *Job
	Status StatisticsStatus
}

// StatisticsBuffer buffers job completions and flushes them in batches
// This is aligned with PgQueuer's JobBuffer design for performance optimization
type StatisticsBuffer struct {
	maxSize       int
	timeout       time.Duration
	events        []JobCompletion
	mu            sync.Mutex
	lastEventTime time.Time
	flushCallback func(context.Context, []JobCompletion) error
	stopCh        chan struct{}
	wg            sync.WaitGroup
	logger        *slog.Logger
}

// NewStatisticsBuffer creates a new statistics buffer
// maxSize: maximum number of events before auto-flush (like PgQueuer's max_size)
// timeout: maximum time before auto-flush (like PgQueuer's timeout)
// flushCallback: function to call when flushing (like PgQueuer's flush_callback)
func NewStatisticsBuffer(maxSize int, timeout time.Duration, flushCallback func(ctx context.Context, stats []JobCompletion) error, logger *slog.Logger) *StatisticsBuffer {
	if logger == nil {
		logger = slog.Default()
	}

	sb := &StatisticsBuffer{
		maxSize:       maxSize,
		timeout:       timeout,
		events:        make([]JobCompletion, 0, maxSize),
		lastEventTime: time.Now(),
		flushCallback: flushCallback,
		stopCh:        make(chan struct{}),
		logger:        logger,
	}

	// Start monitor goroutine (like PgQueuer's buffer.monitor())
	sb.wg.Add(1)
	go sb.monitor()

	return sb
}

// Add adds a job completion to the buffer
// Similar to PgQueuer's buffer.add_job(job, status)
func (sb *StatisticsBuffer) Add(completion JobCompletion) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	sb.events = append(sb.events, completion)
	sb.lastEventTime = time.Now()

	// If buffer is full, flush immediately (like PgQueuer)
	if len(sb.events) >= sb.maxSize {
		sb.logger.Debug("Buffer full, flushing", "size", len(sb.events))
		return sb.flushLocked()
	}

	return nil
}

// flushLocked flushes buffered events (must be called with lock held)
func (sb *StatisticsBuffer) flushLocked() error {
	if len(sb.events) == 0 {
		return nil
	}

	// Copy events to avoid holding lock during callback
	events := make([]JobCompletion, len(sb.events))
	copy(events, sb.events)
	sb.events = sb.events[:0] // Clear buffer

	// Release lock before callback to avoid deadlock
	sb.mu.Unlock()
	defer sb.mu.Lock()

	// Call flush callback with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sb.flushCallback(ctx, events); err != nil {
		sb.logger.Error("Failed to flush statistics", "error", err, "count", len(events))
		return err
	}

	sb.logger.Debug("Flushed statistics", "count", len(events))
	return nil
}

// monitor periodically checks if buffer should be flushed due to timeout
// Similar to PgQueuer's buffer.monitor() async task
func (sb *StatisticsBuffer) monitor() {
	defer sb.wg.Done()

	ticker := time.NewTicker(sb.timeout)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sb.mu.Lock()
			// Check if enough time has passed since last event
			if time.Since(sb.lastEventTime) >= sb.timeout && len(sb.events) > 0 {
				sb.logger.Debug("Timeout reached, flushing", "size", len(sb.events))
				sb.flushLocked()
			}
			sb.mu.Unlock()

		case <-sb.stopCh:
			// Final flush on shutdown
			sb.mu.Lock()
			if len(sb.events) > 0 {
				sb.logger.Info("Final flush on shutdown", "size", len(sb.events))
				sb.flushLocked()
			}
			sb.mu.Unlock()
			return
		}
	}
}

// Stop stops the buffer and performs final flush
// Similar to PgQueuer's buffer.alive = False
func (sb *StatisticsBuffer) Stop() error {
	// Use select to avoid panic on already-closed channel
	select {
	case <-sb.stopCh:
		// Already stopped
		return nil
	default:
		close(sb.stopCh)
		sb.wg.Wait()
		return nil
	}
}
