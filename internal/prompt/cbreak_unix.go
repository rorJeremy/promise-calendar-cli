//go:build unix

package prompt

import "golang.org/x/sys/unix"

// setCbreak puts fd into non-canonical mode so Esc is readable at once,
// and turns echo off so this package can echo the answer itself (backspace
// included). Signals stay enabled, so Ctrl-C is still SIGINT, and input
// newline mapping stays, so Enter is still a newline. The bool reports
// whether the terminal actually changed. The returned function puts it back,
// which is what leaves echo on after a cancel.
func setCbreak(fd int) (func(), bool, error) {
	termios, err := unix.IoctlGetTermios(fd, readTermios)
	if err != nil {
		return func() {}, false, err
	}
	old := *termios
	termios.Lflag &^= unix.ICANON | unix.ECHO | unix.ECHONL | unix.IEXTEN
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, writeTermios, termios); err != nil {
		return func() {}, false, err
	}
	return func() {
		_ = unix.IoctlSetTermios(fd, writeTermios, &old)
	}, true, nil
}
