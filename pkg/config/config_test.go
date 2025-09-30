package config

import (
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Test database defaults
	if cfg.Database.MaxConnections != 25 {
		t.Errorf("Expected max_connections to be 25, got %d", cfg.Database.MaxConnections)
	}

	if cfg.Database.MaxIdleTime != 30*time.Minute {
		t.Errorf("Expected max_idle_time to be 30m, got %v", cfg.Database.MaxIdleTime)
	}

	// Test queue defaults
	if cfg.Queue.BatchSize != 10 {
		t.Errorf("Expected batch_size to be 10, got %d", cfg.Queue.BatchSize)
	}

	if cfg.Queue.WorkerCount != 5 {
		t.Errorf("Expected worker_count to be 5, got %d", cfg.Queue.WorkerCount)
	}

	if cfg.Queue.Channel != "pgqueue_events" {
		t.Errorf("Expected channel to be 'pgqueue_events', got %s", cfg.Queue.Channel)
	}

	// Test logging defaults
	if cfg.Logging.Level != "info" {
		t.Errorf("Expected logging level to be 'info', got %s", cfg.Logging.Level)
	}
}
