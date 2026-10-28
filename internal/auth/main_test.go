package auth

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain swaps in go-keyring's in-memory mock before any test runs, so a
// test that forgets to pick a keyring never touches the real one.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
