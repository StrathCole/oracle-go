package tx

import (
	"context"
	"fmt"

	"tc.com/oracle-prices/pkg/feeder/client"

	abcitypes "github.com/cometbft/cometbft/abci/types"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/rs/zerolog"
)

// Broadcaster handles transaction construction, signing, and broadcasting.
type Broadcaster struct {
	client   *client.Client
	keyring  keyring.Keyring
	txConfig sdkclient.TxConfig
	chainID  string
	logger   zerolog.Logger
}

// BroadcasterConfig holds configuration for creating a Broadcaster.
type BroadcasterConfig struct {
	Client   *client.Client
	Keyring  keyring.Keyring
	TxConfig sdkclient.TxConfig
	ChainID  string
	Logger   zerolog.Logger
}

// NewBroadcaster creates a new transaction broadcaster.
func NewBroadcaster(cfg BroadcasterConfig) *Broadcaster {
	return &Broadcaster{
		client:   cfg.Client,
		keyring:  cfg.Keyring,
		txConfig: cfg.TxConfig,
		chainID:  cfg.ChainID,
		logger:   cfg.Logger,
	}
}

// BroadcastTxRequest holds parameters for broadcasting a transaction.
type BroadcastTxRequest struct {
	Msgs      []sdk.Msg      // Messages to include in transaction
	Feeder    sdk.AccAddress // Feeder account (signs the transaction)
	FeeAmount sdk.Coins      // Fee amount
	GasLimit  uint64         // Gas limit
	Memo      string         // Transaction memo (optional)
}

// BroadcastTx constructs, signs, and broadcasts a transaction.
// It automatically retrieves account number and sequence from the chain.
//
// Returns the transaction response if successful, or an error if:
// - Failed to get account info
// - Failed to sign transaction
// - Failed to broadcast transaction
// - Transaction was rejected by the chain (non-zero code)
func (b *Broadcaster) BroadcastTx(ctx context.Context, req BroadcastTxRequest) (*sdk.TxResponse, error) {
	// Get account info (account number and sequence)
	accNum, sequence, err := b.client.GetAccount(ctx, req.Feeder)
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	b.logger.Debug().
		Str("feeder", req.Feeder.String()).
		Uint64("account_number", accNum).
		Uint64("sequence", sequence).
		Int("num_msgs", len(req.Msgs)).
		Msg("Building transaction")

	// Get key info from keyring
	keyInfo, err := b.keyring.KeyByAddress(req.Feeder)
	if err != nil {
		return nil, fmt.Errorf("failed to get key from keyring: %w", err)
	}

	// Create transaction builder
	txBuilder := b.txConfig.NewTxBuilder()

	// Set messages
	if err := txBuilder.SetMsgs(req.Msgs...); err != nil {
		return nil, fmt.Errorf("failed to set messages: %w", err)
	}

	// Set fee and gas
	txBuilder.SetFeeAmount(req.FeeAmount)
	txBuilder.SetGasLimit(req.GasLimit)

	// Set memo if provided
	if req.Memo != "" {
		txBuilder.SetMemo(req.Memo)
	}

	// Create transaction factory
	txFactory := tx.Factory{}.
		WithChainID(b.chainID).
		WithKeybase(b.keyring).
		WithTxConfig(b.txConfig).
		WithAccountNumber(accNum).
		WithSequence(sequence)

	// Sign transaction
	if err := tx.Sign(txFactory, keyInfo.Name, txBuilder, true); err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Encode transaction
	txBytes, err := b.txConfig.TxEncoder()(txBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to encode transaction: %w", err)
	}

	b.logger.Info().
		Uint64("account_number", accNum).
		Uint64("sequence", sequence).
		Uint64("gas_limit", req.GasLimit).
		Str("fee", req.FeeAmount.String()).
		Int("tx_bytes", len(txBytes)).
		Msg("Broadcasting transaction")

	// Broadcast transaction
	txResp, err := b.client.BroadcastTx(ctx, txBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	// Check transaction response code
	if txResp.Code != abcitypes.CodeTypeOK {
		return txResp, fmt.Errorf("transaction rejected: code=%d, log=%s", txResp.Code, txResp.RawLog)
	}

	b.logger.Info().
		Str("tx_hash", txResp.TxHash).
		Uint64("height", uint64(txResp.Height)).
		Uint64("gas_used", uint64(txResp.GasUsed)).
		Uint64("gas_wanted", uint64(txResp.GasWanted)).
		Msg("Transaction broadcast successful")

	return txResp, nil
}

// EstimateGas estimates the gas needed for a transaction.
// This is a simple estimation based on message count.
// For more accurate estimation, you could simulate the transaction.
//
// Returns the estimated gas limit.
func EstimateGas(numMsgs int) uint64 {
	// Base gas for transaction overhead
	const baseGas uint64 = 50000

	// Gas per message (oracle messages are relatively cheap)
	const gasPerMsg uint64 = 50000

	return baseGas + (uint64(numMsgs) * gasPerMsg)
}

// CalculateFee calculates the fee for a transaction given gas limit and gas price.
// Terra Classic uses uluna as the fee denomination.
//
// Returns the fee as sdk.Coins.
func CalculateFee(gasLimit uint64, gasPriceStr string, feeDenom string) (sdk.Coins, error) {
	// Parse gas price
	gasPrice, err := sdk.NewDecFromStr(gasPriceStr)
	if err != nil {
		return nil, fmt.Errorf("invalid gas price: %w", err)
	}

	// Calculate fee: gasLimit * gasPrice
	feeAmount := gasPrice.MulInt64(int64(gasLimit)).Ceil().TruncateInt()

	return sdk.NewCoins(sdk.NewCoin(feeDenom, feeAmount)), nil
}

// DefaultFee returns a default fee for oracle transactions.
// Terra Classic typical fee: 100000 uluna per message
func DefaultFee(numMsgs int) sdk.Coins {
	const feePerMsg = 100000 // 0.1 LUNC per message
	totalFee := int64(numMsgs * feePerMsg)
	return sdk.NewCoins(sdk.NewInt64Coin("uluna", totalFee))
}
