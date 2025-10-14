package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tc.com/oracle-prices/pkg/config"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/metrics"
	"tc.com/oracle-prices/pkg/server/aggregator"
	"tc.com/oracle-prices/pkg/server/api"
	"tc.com/oracle-prices/pkg/server/sources"

	// Import sources to register them
	_ "tc.com/oracle-prices/pkg/server/sources/cex"
)

const version = "0.1.0-dev"

var (
	configFile = flag.String("config", "config/config.yaml", "Path to configuration file")
	showVer    = flag.Bool("version", false, "Show version and exit")
	serverOnly = flag.Bool("server", false, "Run price server only")
	feederOnly = flag.Bool("feeder", false, "Run feeder only")
)

func main() {
	flag.Parse()

	if *showVer {
		fmt.Printf("oracle-go version %s\n", version)
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.Load(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Override mode based on flags
	if *serverOnly {
		cfg.Mode = "server"
	} else if *feederOnly {
		cfg.Mode = "feeder"
	}

	// Validate configuration
	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logging
	logger, err := logging.Init(cfg.Logging.Level, cfg.Logging.Format, cfg.Logging.Output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	logging.SetGlobal(logger)

	logger.Info("Starting oracle-go", "version", version, "mode", cfg.Mode)

	// Initialize metrics
	if cfg.Metrics.Enabled {
		metrics.Init()
		go func() {
			logger.Info("Starting metrics server", "addr", cfg.Metrics.Addr)
			if err := metrics.ServeHTTP(cfg.Metrics.Addr); err != nil {
				logger.Error("Metrics server failed", "error", err)
			}
		}()
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Run based on mode
	errChan := make(chan error, 1)

	if cfg.IsServerMode() {
		logger.Info("Starting in server mode")
		go func() {
			errChan <- runServer(ctx, cfg, logger)
		}()
	}

	if cfg.IsFeederMode() {
		logger.Info("Starting in feeder mode")
		go func() {
			errChan <- runFeeder(ctx, cfg, logger)
		}()
	}

	// Wait for shutdown signal or error
	select {
	case sig := <-sigChan:
		logger.Info("Received shutdown signal", "signal", sig.String())
		cancel()
	case err := <-errChan:
		if err != nil {
			logger.Error("Component failed", "error", err)
			cancel()
		}
	}

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	logger.Info("Shutting down gracefully...")
	<-shutdownCtx.Done()
	logger.Info("Shutdown complete")
}

func runServer(ctx context.Context, cfg *config.Config, logger *logging.Logger) error {
	// Initialize sources
	var allSources []sources.Source

	for _, sourceCfg := range cfg.Sources {
		if !sourceCfg.Enabled {
			continue
		}

		logger.Info("Initializing source", "type", sourceCfg.Type, "name", sourceCfg.Name)

		source, err := sources.Create(sourceCfg.Type, sourceCfg.Name, sourceCfg.Config)
		if err != nil {
			logger.Warn("Failed to create source", "type", sourceCfg.Type, "name", sourceCfg.Name, "error", err)
			continue
		}

		if err := source.Initialize(ctx); err != nil {
			logger.Warn("Failed to initialize source", "source", source.Name(), "error", err)
			continue
		}

		if err := source.Start(ctx); err != nil {
			logger.Warn("Failed to start source", "source", source.Name(), "error", err)
			continue
		}

		allSources = append(allSources, source)
		logger.Info("Source started", "source", source.Name(), "symbols", source.Symbols())
	}

	if len(allSources) == 0 {
		return fmt.Errorf("no sources available")
	}

	// Create aggregator based on configuration
	agg, err := aggregator.NewAggregator(cfg.Server.AggregateMode, logger)
	if err != nil {
		return fmt.Errorf("failed to create aggregator: %w", err)
	}
	logger.Info("Created aggregator", "mode", cfg.Server.AggregateMode)

	// Start HTTP server
	server := api.NewServer(cfg.Server.HTTP.Addr, allSources, agg, cfg.Server.CacheTTL.ToDuration(), logger)

	// Start WebSocket server if enabled
	var wsServer *api.WebSocketServer
	if cfg.Server.WebSocket.Enabled {
		wsServer = api.NewWebSocketServer(cfg.Server.WebSocket.Addr, logger)
		server.SetWebSocketServer(wsServer)

		go func() {
			if err := wsServer.Start(); err != nil {
				logger.Error("WebSocket server error", "error", err)
			}
		}()
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Stop servers
		server.Stop(shutdownCtx)
		if wsServer != nil {
			wsServer.Stop()
		}

		// Stop sources
		for _, source := range allSources {
			source.Stop()
		}
	}()

	return server.Start()
}

func runFeeder(ctx context.Context, cfg *config.Config, logger *logging.Logger) error {
	// TODO: Implement feeder component
	logger.Warn("Feeder component not yet implemented")

	<-ctx.Done()
	return nil
}
