//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package prompt

import "golang.org/x/sys/unix"

const (
	readTermios  = unix.TIOCGETA
	writeTermios = unix.TIOCSETA
)
