package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	vestingtypes "github.com/cosmos/cosmos-sdk/x/auth/vesting/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"tc.com/oracle-prices/pkg/config"
	feederClient "tc.com/oracle-prices/pkg/feeder/client"
	"tc.com/oracle-prices/pkg/feeder/eventstream"
	"tc.com/oracle-prices/pkg/feeder/keystore"
	feedertx "tc.com/oracle-prices/pkg/feeder/tx"
	"tc.com/oracle-prices/pkg/feeder/voter"
	"tc.com/oracle-prices/pkg/logging"
	"tc.com/oracle-prices/pkg/metrics"
	"tc.com/oracle-prices/pkg/server/aggregator"
	"tc.com/oracle-prices/pkg/server/api"
	"tc.com/oracle-prices/pkg/server/sources"

	// Import sources to register them
	_ "tc.com/oracle-prices/pkg/server/sources/cex"
	"tc.com/oracle-prices/pkg/server/sources/cosmwasm"
	_ "tc.com/oracle-prices/pkg/server/sources/evm"
	_ "tc.com/oracle-prices/pkg/server/sources/fiat"
	_ "tc.com/oracle-prices/pkg/server/sources/oracle"
)

const version = "0.1.0-dev"

var (
	configFile = flag.String("config", "config/config.yaml", "Path to configuration file")
	showVer    = flag.Bool("version", false, "Show version and exit")
	serverOnly = flag.Bool("server", false, "Run price server only")
	feederOnly = flag.Bool("feeder", false, "Run feeder only")
	dryRun     = flag.Bool("dry-run", false, "Dry run mode: create votes but don't submit them (for testing)")
	verify     = flag.Bool("verify", false, "Verify votes against on-chain rates (requires --dry-run)")
)

func main() {
	flag.Parse()

	if *showVer {
		fmt.Printf("oracle-go version %s\n", version)
		os.Exit(0)
	}

	// Configure Cosmos SDK with Terra Classic prefixes
	// This must be done before any other SDK operations
	sdkConfig := sdk.GetConfig()
	sdkConfig.SetBech32PrefixForAccount("terra", "terrapub")
	sdkConfig.SetBech32PrefixForValidator("terravaloper", "terravaloperpub")
	sdkConfig.SetBech32PrefixForConsensusNode("terravalcons", "terravalconspub")
	sdkConfig.SetCoinType(330) // Terra Classic coin type
	sdkConfig.SetPurpose(44)   // BIP44 purpose
	sdkConfig.Seal()           // Make config immutable

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

	// Override dry-run setting from command line
	if *dryRun {
		cfg.Feeder.DryRun = true
	}

	// Override verify setting from command line
	if *verify {
		if !*dryRun && !cfg.Feeder.DryRun {
			fmt.Fprintf(os.Stderr, "Error: --verify requires --dry-run to be enabled\n")
			os.Exit(1)
		}
		cfg.Feeder.Verify = true
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

	// Log dry-run and verify mode if enabled
	if cfg.Feeder.DryRun {
		logger.Warn("DRY RUN MODE ENABLED - Votes will be created but NOT submitted to the blockchain")
	}

	if cfg.Feeder.Verify {
		if !cfg.Feeder.DryRun {
			logger.Error("VERIFICATION requires DRY-RUN mode - this should have been caught earlier")
		} else {
			logger.Info("VERIFICATION ENABLED - Votes will be compared against on-chain exchange rates")
		}
	}

	// Initialize metrics (only if running server mode)
	if cfg.Metrics.Enabled && cfg.IsServerMode() {
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
	// Initialize gRPC client for CosmWasm sources if any are enabled
	var grpcClient *feederClient.Client
	for _, sourceCfg := range cfg.Sources {
		if sourceCfg.Enabled && sourceCfg.Type == "cosmwasm" {
			// Need to create gRPC client
			// Convert GRPCEndpoint structs to EndpointConfig
			var endpoints []feederClient.EndpointConfig
			for _, ep := range cfg.Feeder.GRPCEndpoints {
				endpoints = append(endpoints, feederClient.EndpointConfig{
					Address: ep.ToAddress(),
					TLS:     ep.TLS,
				})
			}

			ir := codectypes.NewInterfaceRegistry()
			clientCfg := feederClient.ClientConfig{
				Endpoints:         endpoints,
				ChainID:           cfg.Feeder.ChainID,
				InterfaceRegistry: ir,
				Logger:            logger.ZerologLogger(),
			}

			var err error
			grpcClient, err = feederClient.NewClient(clientCfg)
			if err != nil {
				logger.Warn("Failed to create gRPC client for CosmWasm sources", "error", err)
			} else {
				// Set the global gRPC client for CosmWasm sources
				cosmwasm.SetGRPCClient(grpcClient)

				// Log endpoint addresses
				var addrs []string
				for _, e := range endpoints {
					addrs = append(addrs, e.Address)
				}
				logger.Info("Initialized gRPC client for CosmWasm sources", "endpoints", addrs)
			}
			break
		}
	}

	// Initialize sources
	var allSources []sources.Source
	sourceWeights := make(map[string]float64) // Track weights for aggregation

	for _, sourceCfg := range cfg.Sources {
		if !sourceCfg.Enabled {
			continue
		}

		logger.Info("Initializing source", "type", sourceCfg.Type, "name", sourceCfg.Name, "weight", sourceCfg.Weight)

		// Add logger to config so sources don't create their own
		if sourceCfg.Config == nil {
			sourceCfg.Config = make(map[string]interface{})
		}
		sourceCfg.Config["logger"] = logger

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

		// Store weight (default to 1.0 if not specified)
		weight := sourceCfg.Weight
		if weight == 0 {
			weight = 1.0
		}
		sourceWeights[source.Name()] = weight

		logger.Info("Source started", "source", source.Name(), "symbols", source.Symbols(), "weight", weight)
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
	server := api.NewServer(cfg.Server.HTTP.Addr, allSources, agg, sourceWeights, cfg.Server.CacheTTL.ToDuration(), logger)

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

		// Close gRPC client if initialized
		if grpcClient != nil {
			grpcClient.Close()
		}
	}()

	return server.Start()
}

func runFeeder(ctx context.Context, cfg *config.Config, logger *logging.Logger) error {
	logger.Info("Initializing feeder component",
		"chain_id", cfg.Feeder.ChainID,
		"validators", len(cfg.Feeder.Validators),
		"grpc_endpoints", len(cfg.Feeder.GRPCEndpoints))

	// Get mnemonic (from config or environment variable)
	mnemonic := cfg.Feeder.Mnemonic
	if cfg.Feeder.MnemonicEnv != "" {
		mnemonic = os.Getenv(cfg.Feeder.MnemonicEnv)
		if mnemonic == "" {
			return fmt.Errorf("environment variable %s not set", cfg.Feeder.MnemonicEnv)
		}
	}
	if mnemonic == "" {
		return fmt.Errorf("no mnemonic configured")
	}

	// Determine HD derivation path
	hdPath := cfg.Feeder.HDPath
	if hdPath == "" {
		// Default to Terra Classic path based on coin type
		coinType := cfg.Feeder.CoinType
		if coinType == 0 {
			coinType = 330 // Terra Classic default
		}
		hdPath = fmt.Sprintf("m/44'/%d'/0'/0/0", coinType)
	}
	logger.Info("Using HD derivation path", "path", hdPath)

	// Create keyring from mnemonic
	kr, _, feederAddr := keystore.GetAuth(mnemonic, hdPath)
	logger.Info("Loaded feeder account", "address", feederAddr.String())

	// Create encoding config (includes all registered account types)
	encCfg := makeEncodingConfig()

	// Convert GRPCEndpoint structs to EndpointConfig
	var endpoints []feederClient.EndpointConfig
	for _, ep := range cfg.Feeder.GRPCEndpoints {
		endpoints = append(endpoints, feederClient.EndpointConfig{
			Address: ep.ToAddress(),
			TLS:     ep.TLS,
		})
	}

	// Create gRPC client with properly configured interface registry
	clientCfg := feederClient.ClientConfig{
		Endpoints:         endpoints,
		ChainID:           cfg.Feeder.ChainID,
		InterfaceRegistry: encCfg.InterfaceRegistry,
		Logger:            logger.ZerologLogger(),
	}

	grpcClient, err := feederClient.NewClient(clientCfg)
	if err != nil {
		return fmt.Errorf("failed to create gRPC client: %w", err)
	}
	defer grpcClient.Close()

	// Log endpoint addresses
	var addrs []string
	for _, e := range endpoints {
		addrs = append(addrs, e.Address)
	}
	logger.Info("Connected to gRPC endpoints", "endpoints", addrs)

	// Transaction config was already created above in encCfg
	txConfig := encCfg.TxConfig

	// Create broadcaster
	broadcaster := feedertx.NewBroadcaster(feedertx.BroadcasterConfig{
		Client:   grpcClient,
		Keyring:  kr,
		TxConfig: txConfig,
		ChainID:  cfg.Feeder.ChainID,
		Logger:   logger.ZerologLogger(),
	})

	// Configure Tendermint WebSocket URLs from RPC endpoints
	// Convert RPCEndpoint structs to URLs
	var rpcEndpoints []string
	for _, rpcEp := range cfg.Feeder.RPCEndpoints {
		rpcEndpoints = append(rpcEndpoints, rpcEp.ToURL())
	}
	logger.Info("Using configured RPC endpoints", "endpoints", rpcEndpoints)

	// Create event stream with failover support
	eventStream, err := eventstream.NewStreamWithFailover(rpcEndpoints, grpcClient, logger.ZerologLogger())
	if err != nil {
		return fmt.Errorf("failed to create event stream: %w", err)
	}

	// Start event stream
	if err := eventStream.Start(ctx); err != nil {
		return fmt.Errorf("failed to start event stream: %w", err)
	}

	// Parse fee amount (oracle txs can be free, so 0 amount is valid)
	feeDenom := "uluna" // Default denom
	if cfg.Feeder.FeeAmount != "" {
		logger.Info("Parsing fee configuration", "fee_amount", cfg.Feeder.FeeAmount, "gas_price", cfg.Feeder.GasPrice)

		feeCoins, err := sdk.ParseCoinsNormalized(cfg.Feeder.FeeAmount)
		if err != nil {
			return fmt.Errorf("invalid fee amount '%s': %w", cfg.Feeder.FeeAmount, err)
		}
		if len(feeCoins) > 0 {
			feeDenom = feeCoins[0].Denom
		}
	} else {
		logger.Info("No fee amount configured, using zero fees (oracle txs are free)")
	}

	// Create voter
	voterCfg := voter.Config{
		ChainID:       cfg.Feeder.ChainID,
		Validators:    cfg.Feeder.Validators,
		Feeder:        feederAddr.String(),
		PriceSource:   cfg.Feeder.PriceSource.URL,
		MaxRetries:    3,
		RetryInterval: 5 * time.Second,
		GasPrice:      cfg.Feeder.GasPrice,
		FeeDenom:      feeDenom,
		DryRun:        cfg.Feeder.DryRun,
		Verify:        cfg.Feeder.Verify,
	}

	v, err := voter.NewVoter(voterCfg, grpcClient, broadcaster, eventStream, logger.ZerologLogger())
	if err != nil {
		return fmt.Errorf("failed to create voter: %w", err)
	}

	logger.Info("Starting oracle voter")

	// Start voter (blocks until context is canceled)
	return v.Start(ctx)
}

// makeEncodingConfig creates an encoding config for transaction encoding
func makeEncodingConfig() EncodingConfig {
	amino := codec.NewLegacyAmino()
	interfaceRegistry := codectypes.NewInterfaceRegistry()

	// Register all standard SDK modules
	std.RegisterLegacyAminoCodec(amino)
	std.RegisterInterfaces(interfaceRegistry)

	// Register auth module types (required for account queries)
	authtypes.RegisterLegacyAminoCodec(amino)
	authtypes.RegisterInterfaces(interfaceRegistry)

	// Register vesting types (Terra Classic uses vesting accounts)
	vestingtypes.RegisterInterfaces(interfaceRegistry)

	// Register bank types
	banktypes.RegisterInterfaces(interfaceRegistry)

	// Register concrete account types explicitly
	interfaceRegistry.RegisterImplementations(
		(*authtypes.AccountI)(nil),
		&authtypes.BaseAccount{},
		&vestingtypes.PeriodicVestingAccount{},
		&vestingtypes.ContinuousVestingAccount{},
		&vestingtypes.DelayedVestingAccount{},
		&vestingtypes.PermanentLockedAccount{},
	)
	interfaceRegistry.RegisterImplementations(
		(*authtypes.GenesisAccount)(nil),
		&authtypes.BaseAccount{},
		&authtypes.ModuleAccount{},
	)

	marshaler := codec.NewProtoCodec(interfaceRegistry)
	txCfg := tx.NewTxConfig(marshaler, tx.DefaultSignModes)

	return EncodingConfig{
		InterfaceRegistry: interfaceRegistry,
		Codec:             marshaler,
		TxConfig:          txCfg,
		Amino:             amino,
	}
}

// EncodingConfig specifies the concrete encoding types to use for a given app.
type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}
