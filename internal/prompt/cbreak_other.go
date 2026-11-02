//go:build !unix

package prompt

// setCbreak is a no-op where there is no termios. Esc still cancels once the
// byte can be read; a Windows console keeps its own line editing.
func setCbreak(int) (func(), bool, error) {
	return func() {}, false, nil
}
