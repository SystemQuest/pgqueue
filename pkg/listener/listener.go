package listener

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EventHandler is a function that handles received events
type EventHandler func(*Event) error

// Listener manages PostgreSQL LISTEN/NOTIFY connections and event routing
type Listener struct {
	pool         *pgxpool.Pool
	logger       *slog.Logger
	channel      string
	handlers     map[string][]EventHandler
	mu           sync.RWMutex
	conn         *pgx.Conn
	poolConn     *pgxpool.Conn // Keep reference to release back to pool
	connMu       sync.Mutex
	closed       bool
	closeCh      chan struct{}
	eventCh      chan *Event // Event queue for async dispatch (Phase 2)
	eventChSize  int         // Size of event queue buffer
	reconnecting bool        // Track reconnection state
}

// NewListener creates a new PostgreSQL event listener
func NewListener(pool *pgxpool.Pool, channel string, logger *slog.Logger) *Listener {
	if logger == nil {
		logger = slog.Default()
	}

	// Phase 2: Add event queue with buffer size 100
	eventChSize := 100

	return &Listener{
		pool:        pool,
		logger:      logger,
		channel:     channel,
		handlers:    make(map[string][]EventHandler),
		closeCh:     make(chan struct{}),
		eventCh:     make(chan *Event, eventChSize),
		eventChSize: eventChSize,
	}
}

// AddHandler adds an event handler for a specific operation
func (l *Listener) AddHandler(operation Operation, handler EventHandler) {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := string(operation)
	l.handlers[key] = append(l.handlers[key], handler)
	l.logger.Debug("Added event handler", "operation", operation)
}

// AddHandlerAll adds an event handler for all operations
func (l *Listener) AddHandlerAll(handler EventHandler) {
	l.AddHandler(OperationInsert, handler)
	l.AddHandler(OperationUpdate, handler)
	l.AddHandler(OperationDelete, handler)
	l.AddHandler(OperationTruncate, handler)
}

// Start begins listening for PostgreSQL notifications
func (l *Listener) Start(ctx context.Context) error {
	l.logger.Info("Starting PostgreSQL event listener", "channel", l.channel)

	// Create dedicated connection for listening
	if err := l.connect(ctx); err != nil {
		return fmt.Errorf("failed to create listener connection: %w", err)
	}

	// Start listening on the channel
	if _, err := l.conn.Exec(ctx, "LISTEN "+l.channel); err != nil {
		l.conn.Close(ctx)
		return fmt.Errorf("failed to listen on channel %s: %w", l.channel, err)
	}

	l.logger.Info("Listening on PostgreSQL channel", "channel", l.channel)

	// Phase 2: Start event dispatcher goroutine
	go l.eventDispatcher(ctx)

	// Start the event loop
	go l.eventLoop(ctx)

	return nil
}

// Stop stops the event listener
func (l *Listener) Stop(ctx context.Context) error {
	l.connMu.Lock()
	defer l.connMu.Unlock()

	if l.closed {
		return nil
	}

	l.closed = true
	close(l.closeCh)

	if l.conn != nil {
		// Unlisten from the channel (best effort)
		if _, err := l.conn.Exec(ctx, "UNLISTEN "+l.channel); err != nil {
			l.logger.Warn("Failed to unlisten from channel", "channel", l.channel, "error", err)
		}
		l.conn = nil
	}

	// Release connection back to pool - this is critical!
	if l.poolConn != nil {
		l.poolConn.Release()
		l.poolConn = nil
	}

	l.logger.Info("PostgreSQL event listener stopped", "channel", l.channel)
	return nil
}

// connect establishes a dedicated connection for listening
func (l *Listener) connect(ctx context.Context) error {
	l.connMu.Lock()
	defer l.connMu.Unlock()

	if l.conn != nil {
		return nil // Already connected
	}

	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection from pool: %w", err)
	}

	// Save both the pool connection and underlying connection
	// We need poolConn to release back to pool later
	l.poolConn = conn
	l.conn = conn.Conn()

	return nil
}

// reconnect attempts to reconnect after a connection failure with exponential backoff
func (l *Listener) reconnect(ctx context.Context) error {
	l.connMu.Lock()
	if l.reconnecting {
		l.connMu.Unlock()
		return fmt.Errorf("reconnection already in progress")
	}
	l.reconnecting = true
	l.connMu.Unlock()

	defer func() {
		l.connMu.Lock()
		l.reconnecting = false
		l.connMu.Unlock()
	}()

	l.logger.Warn("Starting reconnection with exponential backoff")

	// Release old connection
	l.connMu.Lock()
	if l.conn != nil {
		l.conn = nil
	}
	if l.poolConn != nil {
		l.poolConn.Release()
		l.poolConn = nil
	}
	l.connMu.Unlock()

	// Phase 2: Exponential backoff retry logic
	backoff := time.Second
	maxBackoff := 30 * time.Second
	maxAttempts := 5

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		l.logger.Info("Reconnection attempt", "attempt", attempt, "max_attempts", maxAttempts, "backoff", backoff)

		// Wait with backoff
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return fmt.Errorf("reconnection cancelled: %w", ctx.Err())
		case <-l.closeCh:
			return fmt.Errorf("listener is closed")
		}

		// Attempt to connect
		if err := l.connect(ctx); err != nil {
			l.logger.Error("Reconnection failed", "attempt", attempt, "error", err)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		// Re-listen on the channel
		if _, err := l.conn.Exec(ctx, "LISTEN "+l.channel); err != nil {
			l.logger.Error("Re-listen failed", "attempt", attempt, "error", err)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		l.logger.Info("Successfully reconnected listener", "channel", l.channel, "attempts", attempt)
		return nil
	}

	return fmt.Errorf("failed to reconnect after %d attempts", maxAttempts)
}

// eventLoop is the main event processing loop
func (l *Listener) eventLoop(ctx context.Context) {
	defer l.logger.Debug("Event loop stopped")

	for {
		select {
		case <-ctx.Done():
			l.logger.Debug("Context cancelled, stopping event loop")
			return
		case <-l.closeCh:
			l.logger.Debug("Listener closed, stopping event loop")
			return
		default:
			if err := l.processNotifications(ctx); err != nil {
				l.logger.Error("Error processing notifications", "error", err)

				// Attempt to reconnect
				if err := l.reconnect(ctx); err != nil {
					l.logger.Error("Failed to reconnect", "error", err)
					// Wait before trying again
					select {
					case <-time.After(5 * time.Second):
					case <-ctx.Done():
						return
					case <-l.closeCh:
						return
					}
				}
			}
		}
	}
}

// processNotifications processes incoming PostgreSQL notifications
func (l *Listener) processNotifications(ctx context.Context) error {
	l.connMu.Lock()
	conn := l.conn
	l.connMu.Unlock()

	if conn == nil {
		return fmt.Errorf("no connection available")
	}

	// Wait for notification with timeout
	notification, err := conn.WaitForNotification(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for notification: %w", err)
	}

	l.logger.Debug("Received notification",
		"channel", notification.Channel,
		"payload", notification.Payload)

	// Parse the event (Received timestamp set in ParseEvent)
	event, err := ParseEvent(notification.Payload)
	if err != nil {
		l.logger.Error("Failed to parse event", "error", err, "payload", notification.Payload)
		return nil // Continue processing other events
	}

	// Phase 2: Queue event for async dispatch
	select {
	case l.eventCh <- event:
		l.logger.Debug("Event queued for dispatch", "operation", event.Operation, "queue_size", len(l.eventCh))
	default:
		// Event queue is full - dispatch synchronously as fallback
		l.logger.Warn("Event queue full, dispatching synchronously", "queue_size", l.eventChSize)
		l.dispatchEvent(event)
	}

	return nil
}

// eventDispatcher runs in a goroutine to dispatch events from the queue
// Phase 2: Async event dispatch for better throughput
func (l *Listener) eventDispatcher(ctx context.Context) {
	l.logger.Debug("Event dispatcher started")
	defer l.logger.Debug("Event dispatcher stopped")

	for {
		select {
		case <-ctx.Done():
			l.logger.Debug("Context cancelled, stopping event dispatcher")
			// Drain remaining events
			l.drainEventQueue()
			return

		case <-l.closeCh:
			l.logger.Debug("Listener closed, stopping event dispatcher")
			// Drain remaining events
			l.drainEventQueue()
			return

		case event := <-l.eventCh:
			l.dispatchEvent(event)
		}
	}
}

// drainEventQueue processes remaining events in the queue during shutdown
func (l *Listener) drainEventQueue() {
	l.logger.Debug("Draining event queue", "remaining", len(l.eventCh))

	for {
		select {
		case event := <-l.eventCh:
			l.dispatchEvent(event)
		default:
			l.logger.Debug("Event queue drained")
			return
		}
	}
}

// dispatchEvent sends the event to all registered handlers
func (l *Listener) dispatchEvent(event *Event) {
	l.mu.RLock()
	handlers := l.handlers[string(event.Operation)]
	l.mu.RUnlock()

	if len(handlers) == 0 {
		l.logger.Debug("No handlers for operation", "operation", event.Operation)
		return
	}

	l.logger.Debug("Dispatching event to handlers",
		"operation", event.Operation,
		"handler_count", len(handlers),
		"latency", event.Latency())

	// Call handlers concurrently
	for _, handler := range handlers {
		go func(h EventHandler) {
			if err := h(event); err != nil {
				l.logger.Error("Event handler failed",
					"error", err,
					"operation", event.Operation)
			}
		}(handler)
	}
}

// IsConnected returns whether the listener has an active connection
func (l *Listener) IsConnected() bool {
	l.connMu.Lock()
	defer l.connMu.Unlock()
	return l.conn != nil && !l.closed
}
