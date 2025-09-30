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
	pool     *pgxpool.Pool
	logger   *slog.Logger
	channel  string
	handlers map[string][]EventHandler
	mu       sync.RWMutex
	conn     *pgx.Conn
	connMu   sync.Mutex
	closed   bool
	closeCh  chan struct{}
}

// NewListener creates a new PostgreSQL event listener
func NewListener(pool *pgxpool.Pool, channel string, logger *slog.Logger) *Listener {
	if logger == nil {
		logger = slog.Default()
	}

	return &Listener{
		pool:     pool,
		logger:   logger,
		channel:  channel,
		handlers: make(map[string][]EventHandler),
		closeCh:  make(chan struct{}),
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
		// Unlisten from the channel
		if _, err := l.conn.Exec(ctx, "UNLISTEN "+l.channel); err != nil {
			l.logger.Warn("Failed to unlisten from channel", "channel", l.channel, "error", err)
		}

		// Close the connection
		if err := l.conn.Close(ctx); err != nil {
			l.logger.Warn("Failed to close listener connection", "error", err)
		}
		l.conn = nil
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

	// Use the underlying connection for listening
	l.conn = conn.Conn()

	// Don't release the connection back to the pool - we need it for listening
	// conn.Release() // Don't call this

	return nil
}

// reconnect attempts to reconnect after a connection failure
func (l *Listener) reconnect(ctx context.Context) error {
	l.logger.Warn("Attempting to reconnect listener")

	l.connMu.Lock()
	if l.conn != nil {
		l.conn.Close(ctx)
		l.conn = nil
	}
	l.connMu.Unlock()

	// Wait a bit before reconnecting
	select {
	case <-time.After(1 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	case <-l.closeCh:
		return fmt.Errorf("listener is closed")
	}

	// Attempt to reconnect
	if err := l.connect(ctx); err != nil {
		return err
	}

	// Re-listen on the channel
	if _, err := l.conn.Exec(ctx, "LISTEN "+l.channel); err != nil {
		return fmt.Errorf("failed to re-listen on channel %s: %w", l.channel, err)
	}

	l.logger.Info("Successfully reconnected listener", "channel", l.channel)
	return nil
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

	// Parse the event
	event, err := ParseEvent(notification.Payload)
	if err != nil {
		l.logger.Error("Failed to parse event", "error", err, "payload", notification.Payload)
		return nil // Continue processing other events
	}

	// Dispatch to handlers
	l.dispatchEvent(event)

	return nil
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
