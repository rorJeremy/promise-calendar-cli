//go:build unix

package prompt

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pcal catches SIGTERM itself and ends the prompt's context, so the drawn
// form must leave the signal to it. bubbletea's own handler would turn
// SIGTERM into a quit, which huh reports as an answer: whatever was typed
// so far (Gate 3b F2).
func TestPasswordLeavesSIGTERMToTheContext(t *testing.T) {
	// Stands in for pcal's handler, so the signal doesn't end the test.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(signals) })

	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	out := &syncBuffer{}
	keys := &pausingReader{chunks: []string{"pcal_go", "\r"}, pause: 4 * time.Second}
	go func() {
		for !strings.Contains(out.String(), "*") {
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		<-signals
		// pcal's handler ends the context; give bubbletea's a moment to
		// act first, as it would.
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	got, err := Prompter{In: keys, Out: out, options: drawn}.Password(ctx, "Token")

	require.ErrorIs(t, err, ErrCanceled)
	require.Empty(t, got)
}
