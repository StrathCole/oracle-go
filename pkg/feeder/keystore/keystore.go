package keystore

import (
	"encoding/hex"
	"fmt"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/go-bip39"
)

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
// Returns the keyring, validator address, and account address.
// The HD derivation path should be in format: m/44'/cointype'/account'/change/index
// For Terra Classic: m/44'/330'/0'/0/0
// For standard Cosmos: m/44'/118'/0'/0/0
func GetAuth(mnemonic string, hdPath string) (keyring.Keyring, sdk.ValAddress, sdk.AccAddress) {
	// Default to Terra Classic path if not specified
	if hdPath == "" {
		hdPath = "m/44'/330'/0'/0/0"
	}

	seed := bip39.NewSeed(mnemonic, "")
	master, ch := hd.ComputeMastersFromSeed(seed)

	priv, err := hd.DerivePrivateKeyForPath(master, ch, hdPath)
	if err != nil {
		panic(err)
	}
	kr := newPrivKeyKeyring(hex.EncodeToString(priv))
	return kr, sdk.ValAddress(kr.addr), kr.addr
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
func (p privKeyKeyring) Key(uid string) (*keyring.Record, error) {
	return p.KeyByAddress(p.addr)
}

// KeyByAddress returns the record for the given address.
func (p privKeyKeyring) KeyByAddress(address sdk.Address) (*keyring.Record, error) {
	if !address.Equals(p.addr) {
		return nil, fmt.Errorf("key not found: %s", address)
	}

	return keyring.NewLocalRecord(p.addr.String(), p.privKey, p.pubKey)
}

// Sign signs a message with the keyring's private key.
func (p privKeyKeyring) Sign(uid string, msg []byte) ([]byte, cryptotypes.PubKey, error) {
	return p.SignByAddress(p.addr, msg)
}

// SignByAddress signs a message with the private key associated with the given address.
func (p privKeyKeyring) SignByAddress(address sdk.Address, msg []byte) ([]byte, cryptotypes.PubKey, error) {
	if !p.addr.Equals(address) {
		return nil, nil, fmt.Errorf("key not found")
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

func (p privKeyKeyring) Rename(from string, to string) error {
	panic("must never be called")
}

func (p privKeyKeyring) List() ([]*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SupportedAlgorithms() (keyring.SigningAlgoList, keyring.SigningAlgoList) {
	panic("must never be called")
}

func (p privKeyKeyring) Delete(uid string) error {
	panic("must never be called")
}

func (p privKeyKeyring) DeleteByAddress(address sdk.Address) error {
	panic("must never be called")
}

func (p privKeyKeyring) NewMnemonic(
	uid string, language keyring.Language, hdPath, bip39Passphrase string, algo keyring.SignatureAlgo,
) (*keyring.Record, string, error) {
	panic("must never be called")
}

func (p privKeyKeyring) NewAccount(
	uid, mnemonic, bip39Passphrase, hdPath string, algo keyring.SignatureAlgo,
) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SaveLedgerKey(
	uid string, algo keyring.SignatureAlgo, hrp string, coinType, account, index uint32,
) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SaveOfflineKey(uid string, pubkey cryptotypes.PubKey) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SavePubKey(
	uid string, pubkey cryptotypes.PubKey, algo hd.PubKeyType,
) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) SaveMultisig(uid string, pubkey cryptotypes.PubKey) (*keyring.Record, error) {
	panic("must never be called")
}

func (p privKeyKeyring) ExportPubKeyArmor(uid string) (string, error) {
	panic("must never be called")
}

func (p privKeyKeyring) ExportPubKeyArmorByAddress(address sdk.Address) (string, error) {
	panic("must never be called")
}

func (p privKeyKeyring) ExportPrivKeyArmor(uid, encryptPassphrase string) (armor string, err error) {
	panic("must never be called")
}

func (p privKeyKeyring) ExportPrivKeyArmorByAddress(address sdk.Address, encryptPassphrase string) (armor string, err error) {
	panic("must never be called")
}

// ===== Null interfaces for compile-time checking =====

var _ keyring.Importer = (*ImporterNull)(nil)

type ImporterNull struct{}

func (i ImporterNull) ImportPrivKey(uid, armor, passphrase string) error {
	panic("must never be called")
}

func (i ImporterNull) ImportPrivKeyHex(uid, privKey, algoStr string) error {
	panic("must never be called")
}

func (i ImporterNull) ImportPubKey(uid string, armor string) error {
	panic("must never be called")
}

var _ keyring.Migrator = (*MigratorNull)(nil)

type MigratorNull struct{}

func (m MigratorNull) MigrateAll() ([]*keyring.Record, error) {
	panic("must never be called")
}
