package prompt

import (
	"bytes"
	"io"
)

// lineReader returns at most one line from in on each Read. huh's accessible
// text prompt wraps its reader in a bufio.Scanner, and that scanner fills a
// 4096-byte buffer (huh v2.0.3 internal/accessibility/accessibility.go
// PromptString). A second field's scanner would then see EOF and keep its
// default. Reading one line at a time leaves the later answers in in.
type lineReader struct {
	in   io.Reader
	hold []byte
}

// Read returns the next unread line, including its newline, or whatever
// remains if in ends first.
func (r *lineReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	empty := 0
	for bytes.IndexByte(r.hold, '\n') < 0 {
		var b [1]byte
		n, err := r.in.Read(b[:])
		if n > 0 {
			r.hold = append(r.hold, b[0])
			empty = 0
		}
		if err != nil {
			if len(r.hold) == 0 {
				return 0, err
			}
			break
		}
		if n == 0 {
			empty++
			if empty > 100 {
				return 0, io.ErrNoProgress
			}
		}
	}
	end := bytes.IndexByte(r.hold, '\n')
	if end >= 0 {
		end++
	} else {
		end = len(r.hold)
	}
	if end > len(p) {
		end = len(p)
	}
	n := copy(p, r.hold[:end])
	r.hold = r.hold[n:]
	return n, nil
}
