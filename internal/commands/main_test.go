package commands

import (
	"fmt"
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain is a safety net, so no test can reach the real keychain or the
// real config directory, even one that forgets to set them up: go-keyring's
// in-memory mock replaces the OS keyring, and HOME points at a temporary
// directory. The mock is a package-global map without a lock, so no test
// here runs in parallel.
func TestMain(m *testing.M) {
	keyring.MockInit()
	home, err := os.MkdirTemp("", "pcal-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, key := range []string{"XDG_CONFIG_HOME", "PCAL_TOKEN", "PCAL_URL", "PCAL_NO_KEYRING", "ACCESSIBLE"} {
		_ = os.Unsetenv(key)
	}

	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
