//go:build unix

package commands

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// interruptible catches Ctrl-C (SIGINT) and SIGTERM, and its context says
// which one ended it (Gate 3c; basecamp-cli's loginContext). Real signals,
// sent to this test process.
func TestInterruptibleCatchesSIGINTAndSIGTERM(t *testing.T) {
	// A safety net, so a signal interruptible misses fails the test instead
	// of ending the process.
	safety := make(chan os.Signal, 2)
	signal.Notify(safety, syscall.SIGINT, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(safety) })

	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			a := &app{}
			ctx, stop := a.interruptible(context.Background())
			defer stop()

			require.NoError(t, syscall.Kill(os.Getpid(), sig))

			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatalf("%v didn't end the context", sig)
			}
			require.Equal(t, sig == syscall.SIGTERM, terminated(ctx))
			<-safety
		})
	}
}
