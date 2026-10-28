package version

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserAgent(t *testing.T) {
	platform := " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"

	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "development build", version: "dev", want: "pcal/dev" + platform},
		{name: "release build stamped by ldflags", version: "0.1.0", want: "pcal/0.1.0" + platform},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := Version
			t.Cleanup(func() { Version = original })
			Version = tt.version

			require.Equal(t, tt.want, UserAgent())
		})
	}
}

func TestDefaults(t *testing.T) {
	require.Equal(t, "dev", Version)
	require.Equal(t, "none", Commit)
	require.Equal(t, "unknown", Date)
}
