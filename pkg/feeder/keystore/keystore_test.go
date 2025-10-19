package keystore

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/go-bip39"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test mnemonic (DO NOT use in production).
const testMnemonic = "notice oak worry limit wrap speak medal online prefer cluster roof addict wrist behave treat actual wasp year salad speed social layer crew genius"

func TestGetAuth_ValidMnemonic(t *testing.T) {
	// Get auth info from test mnemonic (using default Terra Classic path)
	kr, valAddr, accAddr, err := GetAuth(testMnemonic, "")
	require.NotNil(t, kr, "Keyring should not be nil")
	require.NotEmpty(t, valAddr, "Validator address should not be empty")
	require.NotEmpty(t, accAddr, "Account address should not be empty")
	require.NoError(t, err, "GetAuth should not return error")

	// Verify addresses are valid
	require.True(t, len(valAddr) > 0, "Validator address should not be empty")
	require.True(t, len(accAddr) > 0, "Account address should not be empty")
}

func TestGetAuth_DeterministicKeys(t *testing.T) {
	// Same mnemonic should always produce same keys
	kr1, valAddr1, accAddr1, err1 := GetAuth(testMnemonic, "")
	kr2, valAddr2, accAddr2, err2 := GetAuth(testMnemonic, "")
	require.NoError(t, err1, "GetAuth should not return error")
	require.NoError(t, err2, "GetAuth should not return error")

	// Addresses should match
	assert.Equal(t, valAddr1.String(), valAddr2.String(),
		"Same mnemonic should produce same validator address")
	assert.Equal(t, accAddr1.String(), accAddr2.String(),
		"Same mnemonic should produce same account address")

	// Sign same message with both keyrings
	testMsg := []byte("test message")
	sig1, _, err1 := kr1.Sign("test", testMsg)
	require.NoError(t, err1)
	sig2, _, err2 := kr2.Sign("test", testMsg)
	require.NoError(t, err2)

	// Signatures should be identical
	assert.Equal(t, sig1, sig2, "Same mnemonic should produce same signatures")
}

func TestGetAuth_DifferentMnemonics(t *testing.T) {
	// Different mnemonics should produce different keys
	mnemonic1 := testMnemonic
	mnemonic2, err := generateTestMnemonic()
	require.NoError(t, err)

	_, valAddr1, accAddr1, err1 := GetAuth(mnemonic1, "")
	_, valAddr2, accAddr2, err2 := GetAuth(mnemonic2, "")
	require.NoError(t, err1, "GetAuth should not return error")
	require.NoError(t, err2, "GetAuth should not return error")

	// Addresses should NOT match
	assert.NotEqual(t, valAddr1.String(), valAddr2.String(),
		"Different mnemonics should produce different validator addresses")
	assert.NotEqual(t, accAddr1.String(), accAddr2.String(),
		"Different mnemonics should produce different account addresses")
}

func TestGetAuth_HDPath(t *testing.T) {
	// Verify we can use different HD paths
	// Test with Terra Classic path (330)
	terraPath := "m/44'/330'/0'/0/0"
	_, valAddrTerra, accAddrTerra, errTerra := GetAuth(testMnemonic, terraPath)
	require.NoError(t, errTerra, "GetAuth should not return error")

	// Test with Cosmos Hub path (118)
	cosmosPath := "m/44'/118'/0'/0/0"
	_, valAddrCosmos, accAddrCosmos, errCosmos := GetAuth(testMnemonic, cosmosPath)
	require.NoError(t, errCosmos, "GetAuth should not return error")

	// Different paths should produce different addresses
	assert.NotEqual(t, valAddrTerra.String(), valAddrCosmos.String(),
		"Different HD paths should produce different validator addresses")
	assert.NotEqual(t, accAddrTerra.String(), accAddrCosmos.String(),
		"Different HD paths should produce different account addresses")

	// Default (empty string) should use Terra Classic path (330)
	_, valAddrDefault, accAddrDefault, errDefault := GetAuth(testMnemonic, "")
	require.NoError(t, errDefault, "GetAuth should not return error")
	assert.Equal(t, valAddrTerra.String(), valAddrDefault.String(),
		"Default path should use Terra Classic (m/44'/330'/0'/0/0)")
	assert.Equal(t, accAddrTerra.String(), accAddrDefault.String(),
		"Default path should use Terra Classic (m/44'/330'/0'/0/0)")
}

func TestPrivKeyKeyring_Key(t *testing.T) {
	kr, _, accAddr, err := GetAuth(testMnemonic, "")
	require.NoError(t, err, "GetAuth should not return error")

	// Get key record by name
	record, err := kr.Key("test")
	require.NoError(t, err, "Key() should not return error")
	require.NotNil(t, record, "Key record should not be nil")

	// Verify address matches
	recAddr, err := record.GetAddress()
	require.NoError(t, err)
	assert.Equal(t, accAddr.String(), recAddr.String(), "Record address should match account address")
}

func TestPrivKeyKeyring_KeyByAddress(t *testing.T) {
	kr, _, accAddr, err := GetAuth(testMnemonic, "")
	require.NoError(t, err, "GetAuth should not return error")

	// Get key record by address
	record, err := kr.KeyByAddress(accAddr)
	require.NoError(t, err, "KeyByAddress() should not return error")
	require.NotNil(t, record, "Key record should not be nil")

	// Try with wrong address
	wrongAddr := sdk.AccAddress([]byte("wrong_address"))
	_, err = kr.KeyByAddress(wrongAddr)
	require.Error(t, err, "KeyByAddress() should return error for wrong address")
}

func TestPrivKeyKeyring_Sign(t *testing.T) {
	kr, _, accAddr, err := GetAuth(testMnemonic, "")
	require.NoError(t, err, "GetAuth should not return error")

	// Sign test message
	testMsg := []byte("test message to sign")
	signature, pubKey, err := kr.Sign("test", testMsg)
	require.NoError(t, err, "Signing should not return error")
	require.NotEmpty(t, signature, "Signature should not be empty")
	require.NotNil(t, pubKey, "Public key should not be nil")

	// Verify signature using public key
	valid := pubKey.VerifySignature(testMsg, signature)
	assert.True(t, valid, "Signature should be valid")

	// Verify address from pubkey matches
	assert.Equal(t, accAddr.String(), sdk.AccAddress(pubKey.Address()).String(),
		"Public key from Sign() should match account address")
}

func TestPrivKeyKeyring_SignByAddress(t *testing.T) {
	kr, _, accAddr, err := GetAuth(testMnemonic, "")
	require.NoError(t, err, "GetAuth should not return error")

	// Sign test message by address
	testMsg := []byte("test message to sign")
	signature, pubKey, err := kr.SignByAddress(accAddr, testMsg)
	require.NoError(t, err, "SignByAddress should not return error")
	require.NotEmpty(t, signature, "Signature should not be empty")
	require.NotNil(t, pubKey, "Public key should not be nil")

	// Verify signature
	valid := pubKey.VerifySignature(testMsg, signature)
	assert.True(t, valid, "Signature should be valid")

	// Try signing with wrong address
	wrongAddr := sdk.AccAddress([]byte("wrong_address"))
	_, _, err = kr.SignByAddress(wrongAddr, testMsg)
	require.Error(t, err, "SignByAddress should return error for wrong address")
}

func TestPrivKeyKeyring_PanicsOnUnimplementedMethods(t *testing.T) {
	kr, _, _, err := GetAuth(testMnemonic, "")
	require.NoError(t, err, "GetAuth should not return error")

	// Backend() should panic
	assert.Panics(t, func() {
		kr.Backend()
	}, "Backend() should panic")

	// Rename() should panic
	assert.Panics(t, func() {
		_ = kr.Rename("old", "new")
	}, "Rename() should panic")

	// List() should panic
	assert.Panics(t, func() {
		_, _ = kr.List()
	}, "List() should panic")

	// Delete() should panic
	assert.Panics(t, func() {
		_ = kr.Delete("test")
	}, "Delete() should panic")

	// NewMnemonic() should panic
	assert.Panics(t, func() {
		_, _, _ = kr.NewMnemonic("newkey", 0, "", "", hd.Secp256k1)
	}, "NewMnemonic() should panic")

	// NewAccount() should panic
	assert.Panics(t, func() {
		_, _ = kr.NewAccount("newkey", testMnemonic, "", "", hd.Secp256k1)
	}, "NewAccount() should panic")

	// ExportPubKeyArmor() should panic
	assert.Panics(t, func() {
		_, _ = kr.ExportPubKeyArmor("test")
	}, "ExportPubKeyArmor() should panic")

	// ExportPrivKeyArmor() should panic
	assert.Panics(t, func() {
		_, _ = kr.ExportPrivKeyArmor("test", "password")
	}, "ExportPrivKeyArmor() should panic")
}

// Helper function to generate a random test mnemonic.
func generateTestMnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return "", err
	}
	return bip39.NewMnemonic(entropy)
}

func TestAddressPrefix(t *testing.T) {
	// Note: Address prefix depends on SDK config (terra/cosmos)
	// We just verify it's a valid bech32 address
	_, _, accAddr, err := GetAuth(testMnemonic, "")
	require.NoError(t, err, "GetAuth should not return error")

	addrStr := accAddr.String()
	require.NotEmpty(t, addrStr, "Address should not be empty")
	require.True(t, len(addrStr) > 10, "Address should be at least 10 characters")
}

func BenchmarkGetAuth(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GetAuth(testMnemonic, "")
	}
}

func BenchmarkSign(b *testing.B) {
	kr, _, _, err := GetAuth(testMnemonic, "")
	require.NoError(b, err, "GetAuth should not return error")
	testMsg := []byte("test message to sign")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := kr.Sign("test", testMsg)
		if err != nil {
			b.Fatal(err)
		}
	}
}
