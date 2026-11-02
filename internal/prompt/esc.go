package prompt

import (
	"errors"
	"io"
	"unicode/utf8"
)

// escReader is the keyboard in front of huh's accessible confirm. huh reads
// a line and treats end of input as the default answer, so Esc never reaches
// it: this returns that line, and reports Esc as canceled instead.
//
// Enter, Ctrl-D, y, and n stay what huh's PromptBool does with them. Ctrl-D
// with an empty line is end of input (the default). Ctrl-D after some text
// submits that text, as a canonical terminal does. Backspace edits the line.
type escReader struct {
	in   io.Reader
	echo io.Writer

	pending  []byte
	canceled bool
	eof      bool
}

// Read returns the next line, including its newline. Esc ends the read with
// EOF and records the cancel; the caller turns that into ErrCanceled, because
// huh would otherwise keep the default answer.
func (r *escReader) Read(p []byte) (int, error) {
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if r.canceled || r.eof {
		return 0, io.EOF
	}

	var line []byte
	one := make([]byte, 1)
	for {
		n, err := r.in.Read(one)
		if n == 1 {
			done, stop := r.key(one[0], &line)
			if stop {
				return 0, io.EOF
			}
			if done {
				break
			}
			continue
		}
		if err == nil {
			err = io.ErrNoProgress
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			line = append(line, '\n')
			break
		}
		if errors.Is(err, io.EOF) {
			r.eof = true
		}
		return 0, err
	}

	n := copy(p, line)
	r.pending = append([]byte(nil), line[n:]...)
	return n, nil
}

// key applies b to line. done means line is ready to return. stop means the
// read ends with EOF: Esc, or Ctrl-D on an empty line.
func (r *escReader) key(b byte, line *[]byte) (done, stop bool) {
	switch b {
	case 0x1b:
		r.canceled = true
		return false, true
	case 0x04:
		if len(*line) == 0 {
			r.eof = true
			return false, true
		}
		*line = append(*line, '\n')
		return true, false
	case '\n', '\r':
		r.write([]byte{'\n'})
		*line = append(*line, '\n')
		return true, false
	case 0x7f, 0x08:
		if len(*line) == 0 {
			return false, false
		}
		_, size := utf8.DecodeLastRune(*line)
		*line = (*line)[:len(*line)-size]
		r.write([]byte("\b \b"))
		return false, false
	default:
		*line = append(*line, b)
		if b >= 0x20 {
			r.write([]byte{b})
		}
		return false, false
	}
}

func (r *escReader) write(p []byte) {
	if r.echo != nil {
		_, _ = r.echo.Write(p)
	}
}
