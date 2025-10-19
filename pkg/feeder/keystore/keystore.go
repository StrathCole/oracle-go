// Package keystore provides key management functionality.
package keystore

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/go-bip39"
)

// ErrKeyNotFound is returned when a key is not found in the keystore.
var ErrKeyNotFound = errors.New("key not found")

var _ keyring.Keyring = (*privKeyKeyring)(nil)

// privKeyKeyring partially implements the keyring.Keyring interface.
// It provides only the functionality needed for transaction signing operations.
// Other methods that are out of scope for transaction signing will panic.
type privKeyKeyring struct {
	addr    sdk.AccAddress
	pubKey  cryptotypes.PubKey
	privKey cryptotypes.PrivKey

	ImporterNull
	MigratorNull
}

// GetAuth derives a keyring from a BIP39 mnemonic using a specified HD derivation path.
// Returns the keyring, validator address, account address, and error.
// The HD derivation path should be in format: m/44'/cointype'/account'/change/index
// For Terra Classic: m/44'/330'/0'/0/0
// For standard Cosmos: m/44'/118'/0'/0/0.
func GetAuth(mnemonic, hdPath string) (keyring.Keyring, sdk.ValAddress, sdk.AccAddress, error) {
	// Default to Terra Classic path if not specified
	if hdPath == "" {
		hdPath = "m/44'/330'/0'/0/0"
	}

	seed := bip39.NewSeed(mnemonic, "")
	master, ch := hd.ComputeMastersFromSeed(seed)

	priv, err := hd.DerivePrivateKeyForPath(master, ch, hdPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to derive private key: %w", err)
	}
	kr := newPrivKeyKeyring(hex.EncodeToString(priv))
	return kr, sdk.ValAddress(kr.addr), kr.addr, nil
}

// newPrivKeyKeyring creates a new keyring from a hex-encoded private key.
func newPrivKeyKeyring(hexKey string) *privKeyKeyring {
	b, err := hex.DecodeString(hexKey)
	if err != nil {
		panic(err)
	}
	key := &secp256k1.PrivKey{
		Key: b,
	}

	return &privKeyKeyring{
		addr:    sdk.AccAddress(key.PubKey().Address()),
		pubKey:  key.PubKey(),
		privKey: key,
	}
}

// Key returns the record for the given key name.
func (p privKeyKeyring) Key(_ string) (*keyring.Record, error) {
	return p.KeyByAddress(p.addr)
}

// KeyByAddress returns the record for the given address.
func (p privKeyKeyring) KeyByAddress(address sdk.Address) (*keyring.Record, error) {
	if !address.Equals(p.addr) {
		return nil, fmt.Errorf("%w: %s", ErrKeyNotFound, address)
	}

	return keyring.NewLocalRecord(p.addr.String(), p.privKey, p.pubKey)
}

// Sign signs a message with the keyring's private key.
func (p privKeyKeyring) Sign(_ string, msg []byte) ([]byte, cryptotypes.PubKey, error) {
	return p.SignByAddress(p.addr, msg)
}

// SignByAddress signs a message with the private key associated with the given address.
func (p privKeyKeyring) SignByAddress(address sdk.Address, msg []byte) ([]byte, cryptotypes.PubKey, error) {
	if !p.addr.Equals(address) {
		return nil, nil, fmt.Errorf("%w", ErrKeyNotFound)
	}

	signed, err := p.privKey.Sign(msg)
	if err != nil {
		return nil, nil, err
	}

	return signed, p.pubKey, nil
}

// ===== Unimplemented methods (must never be called) =====

func (p privKeyKeyring) Backend() string {
	panic("must never be called")
}

func (p privKeyKeyring) Rename(_, _ string) error {
	panic("must never be called")
}

func (p privKeyKeyring) List() ([]*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SupportedAlgorithms() (keyring.SigningAlgoList, keyring.SigningAlgoList) {
	panic("must never be called")
}

func (p privKeyKeyring) Delete(_ string) error {
	panic("must never be called")
}

func (p privKeyKeyring) DeleteByAddress(_ sdk.Address) error {
	panic("must never be called")
}

func (p privKeyKeyring) NewMnemonic(
	_ string, _ keyring.Language, _, _ string, _ keyring.SignatureAlgo,
) (*keyring.Record, string, error) {
	panic("must never be called")
}

func (p privKeyKeyring) NewAccount(
	_, _, _, _ string, _ keyring.SignatureAlgo,
) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SaveLedgerKey(
	_ string, _ keyring.SignatureAlgo, _ string, _, _, _ uint32,
) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SaveOfflineKey(_ string, _ cryptotypes.PubKey) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SavePubKey(
	_ string, _ cryptotypes.PubKey, _ hd.PubKeyType,
) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SaveMultisig(_ string, _ cryptotypes.PubKey) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) ExportPubKeyArmor(_ string) (string, error) {
	panic("must never be called")
}

func (p privKeyKeyring) ExportPubKeyArmorByAddress(_ sdk.Address) (string, error) {
	panic("must never be called")
}

func (p privKeyKeyring) ExportPrivKeyArmor(_, _ string) (string, error) {
	panic("must never be called")
}

func (p privKeyKeyring) ExportPrivKeyArmorByAddress(_ sdk.Address, _ string) (string, error) {
	panic("must never be called")
}

// ===== Null interfaces for compile-time checking =====

var _ keyring.Importer = (*ImporterNull)(nil)

// ImporterNull is a stub implementation of keyring.Importer that panics on any call.
type ImporterNull struct{}

// ImportPrivKey panics - not implemented.
func (i ImporterNull) ImportPrivKey(_, _, _ string) error {
	panic("must never be called")
}

// ImportPrivKeyHex panics - not implemented.
func (i ImporterNull) ImportPrivKeyHex(_, _, _ string) error {
	panic("must never be called")
}

// ImportPubKey panics - not implemented.
func (i ImporterNull) ImportPubKey(_, _ string) error {
	panic("must never be called")
}

var _ keyring.Migrator = (*MigratorNull)(nil)

// MigratorNull is a stub implementation of keyring.Migrator that panics on any call.
type MigratorNull struct{}

// MigrateAll panics - not implemented.
func (m MigratorNull) MigrateAll() ([]*keyring.Record, error) {
	panic("must never be called")
}
