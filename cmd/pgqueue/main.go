package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/systemquest/pgqueue4go/pkg/config"
	"github.com/systemquest/pgqueue4go/pkg/db"
	"github.com/systemquest/pgqueue4go/pkg/queries"
)

const version = "v0.1.0-dev"

var (
	// Global flags
	databaseURL string
	prefix      string
	verbose     bool
	dryRun      bool
	configFile  string

	// Dashboard flags
	interval    int
	tail        int
	tableFormat string
	once        bool

	// Listen flags
	channel string

	// Global config
	appConfig *config.Config
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "pgqueue",
	Short: "PgQueue4Go - PostgreSQL-based job queue system",
	Long: `PgQueue4Go is a high-performance PostgreSQL-based job queue system for Go,
inspired by PgQueuer. It leverages PostgreSQL's LISTEN/NOTIFY and FOR UPDATE SKIP LOCKED
for efficient job processing.`,
	Version: version,
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&databaseURL, "database-url", "", "PostgreSQL connection URL")
	rootCmd.PersistentFlags().StringVar(&prefix, "prefix", "", "Prefix for all database objects")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "Config file path")

	// Initialize config before running commands
	cobra.OnInitialize(initConfig)

	// Add subcommands
	addInstallCommand()
	addUninstallCommand()
	addUpgradeCommand()
	addDashboardCommand()
	addListenCommand()
	addTestCommand()
}

func initConfig() {
	var err error

	// Load config from file and environment
	if configFile != "" {
		appConfig, err = config.LoadWithConfigFile(configFile)
	} else {
		appConfig, err = config.Load()
	}

	if err != nil {
		// Only show warning if we're trying to use a specific config file
		if configFile != "" {
			fmt.Fprintf(os.Stderr, "Warning: Could not load config file %s: %v\n", configFile, err)
		}
		// Use default config if loading fails
		appConfig = &config.Config{
			Database: config.DatabaseConfig{
				URL:            "postgres://localhost:5432/pgqueue?sslmode=disable",
				MaxConnections: 5,
				ConnectTimeout: 10 * time.Second,
			},
			Queue: config.QueueConfig{
				Channel: "pgqueue_events",
			},
		}
	} // Override with command line flags if provided
	if databaseURL != "" {
		appConfig.Database.URL = databaseURL
	}

	// Use environment variables as fallback
	if appConfig.Database.URL == "" {
		if envURL := os.Getenv("DATABASE_URL"); envURL != "" {
			appConfig.Database.URL = envURL
		} else if envURL := os.Getenv("PGDSN"); envURL != "" {
			appConfig.Database.URL = envURL
		}
	}
}

func addInstallCommand() {
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install PgQueue4Go database schema",
		Long: `Creates all necessary database objects including tables, indexes, 
functions, and triggers for PgQueue4Go to operate.`,
		RunE: runInstall,
	}

	installCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print SQL statements without executing them")
	rootCmd.AddCommand(installCmd)
}

func addUninstallCommand() {
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall PgQueue4Go database schema",
		Long: `Removes all PgQueue4Go database objects including tables, indexes,
functions, and triggers. Use with caution as this will delete all job data.`,
		RunE: runUninstall,
	}

	uninstallCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print SQL statements without executing them")
	rootCmd.AddCommand(uninstallCmd)
}

func addUpgradeCommand() {
	upgradeCmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade PgQueue4Go database schema",
		Long: `Upgrades the existing PgQueue4Go database schema to the latest version.
This is safe to run multiple times.`,
		RunE: runUpgrade,
	}

	upgradeCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print SQL statements without executing them")
	rootCmd.AddCommand(upgradeCmd)
}

func addDashboardCommand() {
	dashboardCmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Display real-time queue statistics",
		Long: `Shows a real-time dashboard with queue statistics including job counts,
processing times, and status breakdowns.`,
		RunE: runDashboard,
	}

	dashboardCmd.Flags().IntVarP(&interval, "interval", "i", 5, "Refresh interval in seconds (0 for no refresh)")
	dashboardCmd.Flags().IntVarP(&tail, "tail", "n", 25, "Number of recent log entries to display")
	dashboardCmd.Flags().StringVar(&tableFormat, "table-format", "pretty", "Table format (pretty, simple, grid)")
	dashboardCmd.Flags().BoolVar(&once, "once", false, "Display statistics once and exit")
	rootCmd.AddCommand(dashboardCmd)
}

func addListenCommand() {
	listenCmd := &cobra.Command{
		Use:   "listen",
		Short: "Listen for PostgreSQL notifications",
		Long: `Listens for PostgreSQL NOTIFY events on the specified channel.
Useful for debugging and monitoring queue events.`,
		RunE: runListen,
	}

	listenCmd.Flags().StringVar(&channel, "channel", "", "PostgreSQL NOTIFY channel to listen on")
	rootCmd.AddCommand(listenCmd)
}

func addTestCommand() {
	testCmd := &cobra.Command{
		Use:   "test",
		Short: "Test PgQueue4Go functionality",
		Long: `Runs end-to-end tests to verify PgQueue4Go installation and functionality.
This will create test jobs and verify they are processed correctly.`,
		RunE: runTest,
	}

	rootCmd.AddCommand(testCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func setupLogger() *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
}

func connectDB(ctx context.Context) (*db.DB, error) {
	if appConfig.Database.URL == "" {
		return nil, fmt.Errorf("database URL is required (use --database-url, config file, or DATABASE_URL env var)")
	}

	return db.New(ctx, &appConfig.Database)
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func runInstall(cmd *cobra.Command, args []string) error {
	logger := setupLogger()
	ctx := context.Background()

	// Set prefix if provided
	if prefix != "" {
		os.Setenv("PGQUEUE_PREFIX", prefix)
	}

	// Load configuration to get prefix
	cfg, _ := config.Load()
	actualPrefix := prefix
	if actualPrefix == "" && cfg != nil {
		actualPrefix = cfg.Prefix
	}

	// Create query builder with prefix for dry-run
	var qb *queries.QueryBuilder
	if actualPrefix != "" {
		qb = queries.NewQueryBuilderWithPrefix(actualPrefix)
	} else {
		qb = queries.NewQueryBuilder()
	}

	if dryRun {
		fmt.Println("-- Install SQL (dry run)")
		fmt.Println(qb.CreateInstallQuery())
		return nil
	}

	database, err := connectDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	// Create queries instance with prefix
	var q *queries.Queries
	if actualPrefix != "" {
		q = queries.NewQueriesWithPrefix(database.Pool(), actualPrefix)
	} else {
		q = queries.NewQueries(database.Pool())
	}

	logger.Info("Installing PgQueue4Go schema...")
	if err := q.Install(ctx); err != nil {
		return fmt.Errorf("failed to install schema: %w", err)
	}

	logger.Info("PgQueue4Go schema installed successfully")
	return nil
}

func runUninstall(cmd *cobra.Command, args []string) error {
	logger := setupLogger()
	ctx := context.Background()

	// Set prefix if provided
	if prefix != "" {
		os.Setenv("PGQUEUE_PREFIX", prefix)
	}

	// Load configuration to get prefix
	cfg, _ := config.Load()
	actualPrefix := prefix
	if actualPrefix == "" && cfg != nil {
		actualPrefix = cfg.Prefix
	}

	// Create query builder with prefix for dry-run
	var qb *queries.QueryBuilder
	if actualPrefix != "" {
		qb = queries.NewQueryBuilderWithPrefix(actualPrefix)
	} else {
		qb = queries.NewQueryBuilder()
	}

	if dryRun {
		fmt.Println("-- Uninstall SQL (dry run)")
		fmt.Println(qb.CreateUninstallQuery())
		return nil
	}

	database, err := connectDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	// Create queries instance with prefix
	var q *queries.Queries
	if actualPrefix != "" {
		q = queries.NewQueriesWithPrefix(database.Pool(), actualPrefix)
	} else {
		q = queries.NewQueries(database.Pool())
	}

	logger.Info("Uninstalling PgQueue4Go schema...")
	if err := q.Uninstall(ctx); err != nil {
		return fmt.Errorf("failed to uninstall schema: %w", err)
	}

	logger.Info("PgQueue4Go schema uninstalled successfully")
	return nil
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	logger := setupLogger()
	ctx := context.Background()

	// Set prefix if provided
	if prefix != "" {
		os.Setenv("PGQUEUE_PREFIX", prefix)
	}

	// Load configuration to get prefix
	cfg, _ := config.Load()
	actualPrefix := prefix
	if actualPrefix == "" && cfg != nil {
		actualPrefix = cfg.Prefix
	}

	// Create query builder with prefix for dry-run
	var qb *queries.QueryBuilder
	if actualPrefix != "" {
		qb = queries.NewQueryBuilderWithPrefix(actualPrefix)
	} else {
		qb = queries.NewQueryBuilder()
	}

	if dryRun {
		fmt.Println("-- Upgrade SQL (dry run)")
		upgradeQueries := qb.CreateUpgradeQueries()
		for _, query := range upgradeQueries {
			fmt.Println(query)
			fmt.Println()
		}
		return nil
	}

	database, err := connectDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	// Create queries instance with prefix
	var q *queries.Queries
	if actualPrefix != "" {
		q = queries.NewQueriesWithPrefix(database.Pool(), actualPrefix)
	} else {
		q = queries.NewQueries(database.Pool())
	}

	logger.Info("Upgrading PgQueue4Go schema...")
	if err := q.Upgrade(ctx); err != nil {
		return fmt.Errorf("failed to upgrade schema: %w", err)
	}

	logger.Info("PgQueue4Go schema upgraded successfully")
	return nil
}

func runDashboard(cmd *cobra.Command, args []string) error {
	logger := setupLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("Received shutdown signal")
		cancel()
	}()

	database, err := connectDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	// TODO: Implement dashboard display
	logger.Info("Dashboard functionality coming soon...")
	logger.Info("This will show real-time queue statistics")

	if once {
		return nil
	}

	// Wait for cancellation
	<-ctx.Done()
	return nil
}

func runListen(cmd *cobra.Command, args []string) error {
	logger := setupLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("Received shutdown signal")
		cancel()
	}()

	database, err := connectDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	// Use channel from config if not specified
	listenChannel := channel
	if listenChannel == "" {
		listenChannel = appConfig.Queue.Channel
	}

	logger.Info("Listening for PostgreSQL notifications", "channel", listenChannel)
	logger.Info("Send notifications with: SELECT pg_notify('" + listenChannel + "', 'your_message');")

	// Get a connection for listening
	conn, err := database.Pool().Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	// Start listening
	if _, err := conn.Exec(ctx, "LISTEN "+listenChannel); err != nil {
		return fmt.Errorf("failed to listen on channel %s: %w", listenChannel, err)
	}

	// Listen for notifications
	for {
		select {
		case <-ctx.Done():
			logger.Info("Stopping listener...")
			return nil
		default:
			notification, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil // Context cancelled
				}
				logger.Error("Error waiting for notification", "error", err)
				continue
			}

			logger.Info("Received notification",
				"channel", notification.Channel,
				"payload", notification.Payload,
				"time", time.Now().Format("2006-01-02 15:04:05.000"))
		}
	}
}

func runTest(cmd *cobra.Command, args []string) error {
	logger := setupLogger()
	ctx := context.Background()

	database, err := connectDB(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer database.Close()

	logger.Info("Running PgQueue4Go tests...")

	// TODO: Implement comprehensive tests
	logger.Info("Test functionality coming soon...")
	logger.Info("This will test job creation, processing, and queue operations")

	return nil
}
