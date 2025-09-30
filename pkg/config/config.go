package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	Database DatabaseConfig `mapstructure:"database"`
	Queue    QueueConfig    `mapstructure:"queue"`
	Logging  LoggingConfig  `mapstructure:"logging"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	URL              string        `mapstructure:"url"`
	MaxConnections   int           `mapstructure:"max_connections"`
	MaxIdleTime      time.Duration `mapstructure:"max_idle_time"`
	MaxLifetime      time.Duration `mapstructure:"max_lifetime"`
	ConnectTimeout   time.Duration `mapstructure:"connect_timeout"`
	StatementTimeout time.Duration `mapstructure:"statement_timeout"`
}

// QueueConfig holds queue configuration
type QueueConfig struct {
	BatchSize    int           `mapstructure:"batch_size"`
	WorkerCount  int           `mapstructure:"worker_count"`
	RetryTimeout time.Duration `mapstructure:"retry_timeout"`
	Channel      string        `mapstructure:"channel"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// Load loads configuration from file and environment variables
func Load() (*Config, error) {
	viper.SetConfigName("pgqueue")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("$HOME/.pgqueue")
	viper.AddConfigPath("/etc/pgqueue")

	// Set defaults
	setDefaults()

	// Enable environment variable binding
	viper.AutomaticEnv()
	viper.SetEnvPrefix("PGQUEUE")

	// Read config file if it exists
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}

func setDefaults() {
	// Database defaults
	viper.SetDefault("database.url", "postgres://localhost:5432/pgqueue?sslmode=disable")
	viper.SetDefault("database.max_connections", 25)
	viper.SetDefault("database.max_idle_time", "30m")
	viper.SetDefault("database.max_lifetime", "1h")
	viper.SetDefault("database.connect_timeout", "10s")
	viper.SetDefault("database.statement_timeout", "30s")

	// Queue defaults
	viper.SetDefault("queue.batch_size", 10)
	viper.SetDefault("queue.worker_count", 5)
	viper.SetDefault("queue.retry_timeout", "5m")
	viper.SetDefault("queue.channel", "pgqueue_events")

	// Logging defaults
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "text")
}
