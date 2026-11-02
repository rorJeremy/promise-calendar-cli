//go:build aix || linux || solaris || zos

package prompt

import "golang.org/x/sys/unix"

const (
	readTermios  = unix.TCGETS
	writeTermios = unix.TCSETS
)
