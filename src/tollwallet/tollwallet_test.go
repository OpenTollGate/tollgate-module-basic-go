package tollwallet

import (
	"os"
	"testing"

	"github.com/Origami74/gonuts-tollgate/cashu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Helper function to create a test token
func createTestToken(mint string) cashu.Token {
	return &testToken{mintURL: mint}
}

// Test implementation of cashu.Token
type testToken struct {
	mintURL string
}

func (t *testToken) Mint() string {
	return t.mintURL
}

func (t *testToken) Proofs() cashu.Proofs {
	return cashu.Proofs{}
}

func (t *testToken) Amount() uint64 {
	return 100
}

func (t *testToken) Serialize() (string, error) {
	return "test-token", nil
}

// MockWallet is a mock implementation of the wallet.Wallet interface
type MockWallet struct {
	mock.Mock
}

func (m *MockWallet) Receive(token cashu.Token, swapToTrusted bool) (uint64, error) {
	args := m.Called(token, swapToTrusted)
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockWallet) GetBalance() uint64 {
	args := m.Called()
	return args.Get(0).(uint64)
}

func (m *MockWallet) Send(amount uint64, mintUrl string, includeFees bool) (cashu.Proofs, error) {
	args := m.Called(amount, mintUrl, includeFees)
	return args.Get(0).(cashu.Proofs), args.Error(1)
}

func TestNew(t *testing.T) {
	// Skip this test as it requires network access to real mints
	t.Skip("This test requires network access to real mints")

	// Create a temporary directory for the wallet
	tempDir, err := os.MkdirTemp("", "tollwallet-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	walletPath := tempDir

	// Test case with valid parameters
	t.Run("Valid parameters", func(t *testing.T) {
		acceptedMints := []string{"https://testmint.com"}
		wallet, err := New(walletPath, acceptedMints, false)

		assert.NoError(t, err)
		assert.NotNil(t, wallet)
		assert.NotNil(t, wallet.wallet)
		assert.Equal(t, acceptedMints, wallet.acceptedMints)
	})

	// Test case with no accepted mints
	// Skipping this test as it calls os.Exit which would terminate the test process
	t.Run("No accepted mints", func(t *testing.T) {
		t.Skip("This test would call os.Exit and terminate the test process")

		acceptedMints := []string{}
		_, _ = New(walletPath, acceptedMints, false)
	})
}

func TestReceive(t *testing.T) {
	// Create a manual test for the rejected token case without requiring a real wallet
	t.Run("Direct rejection test for unaccepted mint", func(t *testing.T) {
		// Create a manually constructed TollWallet with fields we control
		tollWallet := &TollWallet{
			// wallet is nil, but we won't use it for this test
			acceptedMints:              []string{"https://accepted-mint.com"},
			allowAndSwapUntrustedMints: false,
		}

		// Create test token from unaccepted mint
		token := createTestToken("https://unaccepted-mint.com")

		// Call the function being tested - should reject before trying to use wallet
		_, err := tollWallet.Receive(token)

		// Assert expectations
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Token rejected")
	})

	// Note: Other tests for Receive would require mocking the wallet.Wallet implementation
	// which is difficult since TollWallet uses a concrete *wallet.Wallet type.
	// In a real application, I would refactor TollWallet to use an interface,
	// allowing for easier testing.
}

// TestSend is skipped because we'd need to mock internal wallet behavior
func TestSend(t *testing.T) {
	t.Skip("Testing Send requires mocking wallet.Send and cashu.NewTokenV4 which is beyond the scope of these tests")
}

// TestGetBalance is skipped because it requires a real wallet implementation
func TestGetBalance(t *testing.T) {
	t.Skip("Testing GetBalance requires mocking wallet.GetBalance which is beyond the scope of these tests")
}

func TestContains(t *testing.T) {
	t.Run("String exists in slice", func(t *testing.T) {
		slice := []string{"apple", "banana", "orange"}
		result := contains(slice, "banana")
		assert.True(t, result)
	})

	t.Run("String does not exist in slice", func(t *testing.T) {
		slice := []string{"apple", "banana", "orange"}
		result := contains(slice, "grape")
		assert.False(t, result)
	})

	t.Run("Empty slice", func(t *testing.T) {
		slice := []string{}
		result := contains(slice, "apple")
		assert.False(t, result)
	})

	t.Run("Case insensitive match", func(t *testing.T) {
		slice := []string{"https://testnut.cashu.exchange"}
		assert.True(t, contains(slice, "https://Testnut.Cashu.Exchange"))
		assert.True(t, contains(slice, "https://TESTNUT.CASHU.EXCHANGE"))
		assert.True(t, contains(slice, "https://testnut.cashu.exchange"))
	})

	t.Run("Mint URL with different casing", func(t *testing.T) {
		mints := []string{"https://mint1.example.com", "https://mint2.example.com"}
		assert.True(t, contains(mints, "https://MINT1.EXAMPLE.COM"))
		assert.True(t, contains(mints, "https://Mint2.Example.Com"))
		assert.False(t, contains(mints, "https://mint3.example.com"))
	})

	t.Run("Case insensitive host but case sensitive path", func(t *testing.T) {
		mints := []string{"https://mint.minibits.cash/Bitcoin"}
		assert.True(t, contains(mints, "https://MINT.MINIBITS.CASH/Bitcoin"),
			"host case should not matter")
		assert.True(t, contains(mints, "https://mint.minibits.cash/Bitcoin"),
			"exact match should work")
		assert.False(t, contains(mints, "https://mint.minibits.cash/bitcoin"),
			"path is case-sensitive")
		assert.False(t, contains(mints, "https://MINT.MINIBITS.CASH/bitcoin"),
			"even with different host case, path must match exactly")
	})

	t.Run("URL parse failure falls back to exact match", func(t *testing.T) {
		mints := []string{"not-a-url"}
		assert.True(t, contains(mints, "not-a-url"))
		assert.False(t, contains(mints, "NOT-A-URL"),
			"fallback is exact match, not EqualFold")
	})

	t.Run("Empty path matches trailing slash (RFC 3986)", func(t *testing.T) {
		mints := []string{"https://testnut.cashu.exchange"}
		assert.True(t, contains(mints, "https://TESTNUT.CASHU.EXCHANGE"))
		assert.True(t, contains(mints, "https://testnut.cashu.exchange/"),
			"empty path == slash path per RFC 3986")
	})
}
