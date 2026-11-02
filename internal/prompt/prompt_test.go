package prompt

import (
	"bytes"
	"context"
	"fmt"
	"image/color"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
)

// testContext bounds a prompt, so a form that hangs fails the test instead
// of the suite.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// pausingReader returns each chunk in its own Read, waiting pause before
// every chunk after the first, as a person typing would. Given everything at
// once, huh handles the keys before it draws a single frame.
type pausingReader struct {
	chunks []string
	pause  time.Duration
	reads  int
}

func (r *pausingReader) Read(b []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	if r.reads > 0 {
		time.Sleep(r.pause)
	}
	r.reads++
	n := copy(b, r.chunks[0])
	r.chunks = r.chunks[1:]
	return n, nil
}

// drawn gives bubbletea what a terminal would: a window size, without which
// it draws nothing into a buffer, and a color profile, which keeps the
// styling.
var drawn = []tea.ProgramOption{tea.WithWindowSize(80, 24), tea.WithColorProfile(colorprofile.ANSI256)}

// Without ACCESSIBLE, huh draws its form and reads keys, so the input is
// keystrokes: Enter is "\r", Ctrl-C "\x03", Esc "\x1b".
func TestPasswordReadsAMaskedAnswer(t *testing.T) {
	var out bytes.Buffer
	keys := &pausingReader{chunks: []string{"pcal_secret", "\r"}, pause: 300 * time.Millisecond}
	p := Prompter{In: keys, Out: &out, options: drawn}

	got, err := p.Password(testContext(t), "Token")

	require.NoError(t, err)
	require.Equal(t, "pcal_secret", got)
	// The form was drawn with the answer masked. bubbletea v2 redraws only
	// what changed, so the *s arrive in pieces; what matters is that they're
	// drawn and no part of the answer is.
	require.Contains(t, out.String(), "Token")
	require.Contains(t, out.String(), "*")
	require.NotContains(t, out.String(), "pcal")
	require.NotContains(t, out.String(), "secret")
}

func TestPasswordCanceled(t *testing.T) {
	tests := []struct {
		name string
		keys string
	}{
		{"Ctrl-C", "pcal_se\x03"},
		// huh binds only Ctrl-C to quit; pcal adds Esc, as basecamp-cli
		// does (internal/tui/forms.go#escKeyMap).
		{"Esc", "pcal_se\x1b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Prompter{In: strings.NewReader(tt.keys), Out: &bytes.Buffer{}}
			ctx := testContext(t)

			got, err := p.Password(ctx, "Token")

			require.ErrorIs(t, err, ErrCanceled)
			require.Empty(t, got)
			// The key canceled it, not the test's deadline.
			require.NoError(t, ctx.Err())
		})
	}
}

// A canceled context (Ctrl-C caught as SIGINT) cancels the prompt.
func TestPasswordCanceledByContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := Prompter{In: strings.NewReader(""), Out: &bytes.Buffer{}}

	_, err := p.Password(ctx, "Token")

	require.ErrorIs(t, err, ErrCanceled)
}

// huh's accessible password field reads the terminal itself, through its
// file descriptor. Given a reader without one it prints nothing and
// answers "" (and given a file that isn't a terminal, prints the prompt,
// then answers ""), so Password says why instead.
func TestAccessiblePasswordNeedsATerminal(t *testing.T) {
	p := Prompter{In: strings.NewReader("pcal_secret\n"), Out: &bytes.Buffer{}, Accessible: true}

	_, err := p.Password(testContext(t), "Token")

	require.EqualError(t, err, "a masked prompt needs a terminal")
}

// Accessible prompts are plain lines, so they can be driven with text.
func TestAccessibleConfirm(t *testing.T) {
	tests := []struct {
		name  string
		input string
		def   bool
		want  bool
	}{
		{"yes", "y\n", false, true},
		{"no", "n\n", true, false},
		{"Enter takes the default (no)", "\n", false, false},
		{"Enter takes the default (yes)", "\n", true, true},
		// huh's accessible confirm treats end of input as the default
		// (accessibility.go PromptString). Ctrl-D is that EOF, so a No
		// default can't be accepted by it (basecamp-cli forms.go:98-112).
		{"Ctrl-D takes the default (no)", "", false, false},
		{"Ctrl-D takes the default (yes)", "", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			p := Prompter{In: strings.NewReader(tt.input), Out: &out, Accessible: true}

			got, err := p.Confirm(testContext(t), "Delete it?", tt.def)

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Contains(t, out.String(), "Delete it?")
		})
	}
}

// When the prompt theme has no attributes, a drawn Confirm's Yes and No
// look the same, so Confirm asks on one plain line instead. Password stays
// drawn: its cursor still shows where the answer goes (Gate 4).
func TestPlainConfirmIsAccessible(t *testing.T) {
	var out bytes.Buffer
	p := Prompter{In: strings.NewReader("y\n"), Out: &out, Plain: true}

	got, err := p.Confirm(testContext(t), `Delete tag "Kids"?`, false)

	require.NoError(t, err)
	require.True(t, got)
	require.Contains(t, out.String(), `Delete tag "Kids"? [y/N]`)
	require.NotContains(t, out.String(), "\x1b")
}

func TestConfirmCanceled(t *testing.T) {
	p := Prompter{In: strings.NewReader("\x03"), Out: &bytes.Buffer{}}

	_, err := p.Confirm(testContext(t), "Delete it?", false)

	require.ErrorIs(t, err, ErrCanceled)
}

// sgrColors returns the Select Graphic Rendition sequences in s that set a
// color: a foreground or background, basic (30-37, 40-47, 90-97, 100-107)
// or extended (38, 48), with ";" or ":" between parameters.
func sgrColors(s string) []string {
	var colored []string
	for _, m := range regexp.MustCompile(`\x1b\[([0-9;:]*)m`).FindAllStringSubmatch(s, -1) {
		for _, param := range strings.Split(m[1], ";") {
			n, err := strconv.Atoi(strings.SplitN(param, ":", 2)[0])
			if err == nil && (n >= 30 && n <= 38 || n >= 40 && n <= 48 || n >= 90 && n <= 97 || n >= 100 && n <= 107) {
				colored = append(colored, m[0])
				break
			}
		}
	}
	return colored
}

func TestSGRColors(t *testing.T) {
	require.Equal(t, []string{"\x1b[38;5;62;1m", "\x1b[31m", "\x1b[1;48:2::1:2:3m"},
		sgrColors("\x1b[1mTitle\x1b[0m \x1b[38;5;62;1mx\x1b[31m\x1b[2m\x1b[7m\x1b[1;48:2::1:2:3m\x1b[m"))
}

// Prompts are drawn with attributes only (bold, faint, reverse), never a
// color, like the rest of pcal's output, so they read the same on a light
// or a dark background. huh's default theme picks colors by the
// background, and bubbles' help line has colors meant for a dark one.
func TestPromptsDrawWithoutColor(t *testing.T) {
	var out bytes.Buffer
	keys := &pausingReader{chunks: []string{"pcal_secret", "\r"}, pause: 300 * time.Millisecond}

	_, err := Prompter{In: keys, Out: &out, options: drawn}.Password(testContext(t), "Token")

	require.NoError(t, err)
	require.Contains(t, out.String(), "\x1b[1mToken", "the title is bold")
	require.Contains(t, out.String(), "submit", "the help line is drawn")
	require.Empty(t, sgrColors(out.String()))
}

// Every style in the theme, for a dark and a light background, including
// the help line and the styles the password prompt doesn't draw (confirm
// buttons, errors), has no color.
func TestThemeHasNoColors(t *testing.T) {
	var colored []string
	for _, plain := range []bool{false, true} {
		for _, dark := range []bool{true, false} {
			path := fmt.Sprintf("plain=%v dark=%v", plain, dark)
			walkStyles(path, reflect.ValueOf(*theme(plain).Theme(dark)), func(path string, style lipgloss.Style) {
				for name, c := range map[string]color.Color{
					"foreground": style.GetForeground(), "background": style.GetBackground(),
					"border top": style.GetBorderTopForeground(), "border right": style.GetBorderRightForeground(),
					"border bottom": style.GetBorderBottomForeground(), "border left": style.GetBorderLeftForeground(),
					"border top background": style.GetBorderTopBackground(), "border right background": style.GetBorderRightBackground(),
					"border bottom background": style.GetBorderBottomBackground(), "border left background": style.GetBorderLeftBackground(),
				} {
					if _, none := c.(lipgloss.NoColor); !none {
						colored = append(colored, fmt.Sprintf("%s %s: %#v", path, name, c))
					}
				}
			})
		}
	}

	require.Empty(t, colored)
}

// walkStyles calls visit for every lipgloss.Style in v, a theme's Styles,
// with its field path.
func walkStyles(path string, v reflect.Value, visit func(string, lipgloss.Style)) {
	if style, ok := v.Interface().(lipgloss.Style); ok {
		visit(path, style)
		return
	}
	if v.Kind() == reflect.Struct {
		for i := range v.NumField() {
			walkStyles(path+"."+v.Type().Field(i).Name, v.Field(i), visit)
		}
	}
}

// Prompts are accessible with ACCESSIBLE set to anything, the switch huh's
// README recommends, and on a dumb terminal, where huh falls back to them
// itself (huh v2.0.3 form.go:129-131), since a redrawing form can't work
// there (Gate 3c).
func TestNewPicksAccessiblePrompts(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want bool
	}{
		{nil, false},
		{map[string]string{"ACCESSIBLE": "1"}, true},
		{map[string]string{"ACCESSIBLE": "true"}, true},
		{map[string]string{"TERM": "dumb"}, true},
		{map[string]string{"TERM": "xterm-256color"}, false},
	}

	for _, tt := range tests {
		p := New(strings.NewReader(""), &bytes.Buffer{}, func(key string) string { return tt.env[key] }, false)
		require.Equal(t, tt.want, p.Accessible, "env %v", tt.env)
	}
}

func TestNewTakesPlain(t *testing.T) {
	getenv := func(string) string { return "" }
	require.True(t, New(strings.NewReader(""), &bytes.Buffer{}, getenv, true).Plain)
	require.False(t, New(strings.NewReader(""), &bytes.Buffer{}, getenv, false).Plain)
}

// When pcal's output is unstyled (NO_COLOR, CLICOLOR=0, TERM=dumb), so are
// its prompts: an accessible prompt prints no escape sequence at all, where
// it would otherwise print its title in bold (Gate 3c).
func TestPlainAccessiblePromptHasNoEscapes(t *testing.T) {
	for _, plain := range []bool{true, false} {
		var out bytes.Buffer
		p := Prompter{In: strings.NewReader("y\n"), Out: &out, Accessible: true, Plain: plain}

		_, err := p.Confirm(testContext(t), "Delete it?", false)

		require.NoError(t, err)
		require.Contains(t, out.String(), "Delete it?")
		if plain {
			require.NotContains(t, out.String(), "\x1b")
		} else {
			require.Contains(t, out.String(), "\x1b[1mDelete it?")
		}
	}
}

// The plain theme sets no attribute anywhere, the help line included.
func TestPlainThemeHasNoAttributes(t *testing.T) {
	var styled []string
	walkStyles("", reflect.ValueOf(*theme(true).Theme(true)), func(path string, s lipgloss.Style) {
		for name, on := range map[string]bool{
			"bold": s.GetBold(), "faint": s.GetFaint(), "reverse": s.GetReverse(), "italic": s.GetItalic(),
			"underline": s.GetUnderline(), "blink": s.GetBlink(), "strikethrough": s.GetStrikethrough(),
		} {
			if on {
				styled = append(styled, path+" "+name)
			}
		}
	})

	require.Empty(t, styled)
}

// A drawn plain prompt has no attribute either, except the cursor's
// reverse cell, which bubbles draws whatever the theme (bubbles v2.0.0
// cursor/cursor.go:240) and which marks where the answer goes.
func TestPlainDrawnPromptHasNoAttributes(t *testing.T) {
	var out bytes.Buffer
	keys := &pausingReader{chunks: []string{"pcal_secret", "\r"}, pause: 300 * time.Millisecond}

	_, err := Prompter{In: keys, Out: &out, Plain: true, options: drawn}.Password(testContext(t), "Token")

	require.NoError(t, err)
	require.Contains(t, out.String(), "Token")
	sgr := map[string]bool{}
	for _, m := range regexp.MustCompile(`\x1b\[[0-9;:]*m`).FindAllString(out.String(), -1) {
		sgr[m] = true
	}
	delete(sgr, "\x1b[m")
	delete(sgr, "\x1b[7m")
	require.Empty(t, sgr)
}

// syncBuffer is a bytes.Buffer safe to read while a form writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
