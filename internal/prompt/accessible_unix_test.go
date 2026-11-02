//go:build linux

package prompt

import (
	"bytes"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// On a real terminal, Esc cancels the accessible confirm before Enter, and
// the terminal is put back afterwards (echo on, canonical). Enter, y, n, and
// Ctrl-D keep huh's answers.
func TestAccessibleConfirmOnATerminal(t *testing.T) {
	tests := []struct {
		name   string
		keys   string
		want   bool
		cancel bool
	}{
		{"Esc", "\x1b", false, true},
		{"y", "y\r", true, false},
		{"n", "n\r", false, false},
		{"Enter", "\r", false, false},
		{"Ctrl-D", "\x04", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			master, slave := openPty(t)
			errCh := make(chan error, 1)
			gotCh := make(chan bool, 1)
			go func() {
				got, err := Prompter{In: slave, Out: slave, Accessible: true}.Confirm(
					testContext(t), `Delete tag "Kids"?`, false)
				gotCh <- got
				errCh <- err
			}()

			waitFor(t, master, "[y/N]")
			_, err := master.Write([]byte(tt.keys))
			require.NoError(t, err)

			select {
			case err := <-errCh:
				got := <-gotCh
				if tt.cancel {
					require.ErrorIs(t, err, ErrCanceled)
				} else {
					require.NoError(t, err)
					require.Equal(t, tt.want, got)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the prompt did not return")
			}

			termios, err := unix.IoctlGetTermios(int(slave.Fd()), readTermios)
			require.NoError(t, err)
			require.NotZero(t, termios.Lflag&unix.ECHO, "echo was left off")
			require.NotZero(t, termios.Lflag&unix.ICANON, "the terminal was left non-canonical")
		})
	}
}

func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = master.Close() })
	require.NoError(t, unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0))
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	require.NoError(t, err)
	name := "/dev/pts/" + strconv.Itoa(n)
	slave, err = os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func waitFor(t *testing.T, master *os.File, text string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got []byte
	for !bytes.Contains(got, []byte(text)) {
		if time.Now().After(deadline) {
			t.Fatalf("prompt never showed %q, saw %q", text, got)
		}
		require.NoError(t, master.SetReadDeadline(time.Now().Add(200*time.Millisecond)))
		buf := make([]byte, 256)
		n, _ := master.Read(buf)
		got = append(got, buf[:n]...)
	}
}
