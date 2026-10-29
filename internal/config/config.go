// Package config finds pcal's config directory, reads and writes
// config.json there, and picks the calendar URL from the flag, the
// environment, the config file, and the default, in that order.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// DefaultURL is the calendar pcal talks to when nothing else is set.
const DefaultURL = "https://calendar.promisechurch.io"

// fileName is the config file's name inside Dir.
const fileName = "config.json"

// Config is what pcal remembers between runs, in config.json.
type Config struct {
	// URL is the calendar's base URL, if one was saved.
	URL string `json:"url,omitempty"`
	// TimeZone is the calendar's time zone (e.g. "America/Chicago"), cached
	// from /my/identity for commands that convert times.
	TimeZone string `json:"time_zone,omitempty"`
}

// Dir returns pcal's config directory: $XDG_CONFIG_HOME/pcal when that's an
// absolute path, otherwise ~/.config/pcal, on macOS too (the design keeps
// one location everywhere, so this isn't os.UserConfigDir).
func Dir(getenv func(string) string) (string, error) {
	// The XDG spec says to ignore a relative path.
	if xdg := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "pcal"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding the config directory: %w", err)
	}
	return filepath.Join(home, ".config", "pcal"), nil
}

// Load reads config.json from dir. A missing file is an empty Config.
func Load(dir string) (Config, error) {
	path := filepath.Join(dir, fileName)
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is pcal's own config file
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return c, nil
}

// Save writes c to config.json in dir (see WriteFile).
func Save(dir string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(filepath.Join(dir, fileName), append(data, '\n'))
}

// WriteFile replaces path, a file in pcal's own config directory, with
// data, readable only by its owner (0600). It creates the directory if
// needed and makes it 0700 even if it already existed with looser
// permissions. It writes a temporary file and renames it over path, so if
// pcal dies partway, path keeps its old contents. It doesn't fsync (nor does
// basecamp/cli's credstore), so a power cut can still lose the write.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory, which needs its execute bit; 0700 is owner-only
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	// After a successful rename this fails harmlessly.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ResolveURL returns the calendar's base URL from, in order: the --url
// flag, PCAL_URL, the saved config, and DefaultURL. It must be https, or
// http to localhost or a loopback address (127.0.0.0/8, ::1), since http
// would send the token unencrypted; it must have a host, and no user name
// or password, query, or fragment. The result is canonical, because it's
// also the key credentials are stored under: a lowercase scheme and host,
// no trailing dot on the host, no default (443, 80) or empty port, and the
// path as escaped, without a trailing slash. A bad URL is a usage error
// (exit 1).
func ResolveURL(flagURL string, getenv func(string) string, c Config) (string, error) {
	raw := DefaultURL
	switch {
	case flagURL != "":
		raw = flagURL
	case getenv("PCAL_URL") != "":
		raw = getenv("PCAL_URL")
	case c.URL != "":
		raw = c.URL
	}
	return normalizeURL(raw)
}

// normalizeURL checks raw and returns it in the canonical form ResolveURL
// describes.
func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", output.Usagef("invalid calendar URL %q: use the form https://calendar.example.com", raw)
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", output.Usagef("invalid calendar URL %q: use the form https://calendar.example.com", raw)
	}
	if u.Scheme == "http" && !isLoopback(host) {
		return "", output.Usagef("invalid calendar URL %q: http would send your token unencrypted, "+
			"so pcal allows it only for localhost, 127.0.0.0/8, and ::1; use https", raw)
	}
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}

	// An IPv6 host goes back in brackets; url.URL escapes a zone's "%"
	// (fe80::1%25en0) when it prints the host.
	canonical := url.URL{Scheme: u.Scheme, Host: host}
	if port != "" {
		canonical.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		canonical.Host = "[" + host + "]"
	}
	return canonical.String() + strings.TrimRight(u.EscapedPath(), "/"), nil
}

// isLoopback reports whether host, lowercase and without brackets, is
// localhost or a loopback IP address (127.0.0.0/8 or ::1).
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
