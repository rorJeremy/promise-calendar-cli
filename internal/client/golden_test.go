package client

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// goldenDir holds the calendar app's golden JSON (`make golden`), the
// API contract this package's wire types are pinned to.
const goldenDir = "../../testdata/api"

// goldenTypes maps each golden file to a new value of its wire type.
var goldenTypes = map[string]func() any{
	"event.json":                   func() any { return new(Event) },
	"events.json":                  func() any { return new([]Occurrence) },
	"tag.json":                     func() any { return new(Tag) },
	"tags.json":                    func() any { return new([]Tag) },
	"rsvps.json":                   func() any { return new([]RSVP) },
	"identity.json":                func() any { return new(Identity) },
	"access_tokens.json":           func() any { return new([]AccessToken) },
	"api_token.json":               func() any { return new(WebsiteToken) },
	"api_token_rotated.json":       func() any { return new(RotatedWebsiteToken) },
	"cli_token.json":               func() any { return new(CLIToken) },
	"error_invalid_date.json":      func() any { return new(APIError) },
	"error_invalid_grant.json":     func() any { return new(APIError) },
	"error_invalid_parameter.json": func() any { return new(APIError) },
	"error_not_found.json":         func() any { return new(APIError) },
	"error_read_only_token.json":   func() any { return new(APIError) },
	"error_unauthorized.json":      func() any { return new(APIError) },
	"error_validation.json":        func() any { return new(ValidationErrors) },
}

// decodeStrict decodes data into v, failing on any field v doesn't have:
// the drift alarm for server-side additions.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return io.ErrUnexpectedEOF // trailing data after the value
	}
	return nil
}

// readGolden returns a golden file's bytes.
func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goldenDir, name))
	require.NoError(t, err)
	return data
}

// TestGoldenFilesHaveTypes fails when the app adds a golden file this table
// doesn't pin, or drops one it does.
func TestGoldenFilesHaveTypes(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(goldenDir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "no golden files in %s; run make golden", goldenDir)

	files := make([]string, 0, len(paths))
	for _, path := range paths {
		files = append(files, filepath.Base(path))
	}
	table := make([]string, 0, len(goldenTypes))
	for name := range goldenTypes {
		table = append(table, name)
	}
	sort.Strings(table)

	require.Equal(t, table, files, "testdata/api and goldenTypes must list the same files")
}

// TestGoldenFilesDecodeStrictlyAndRoundTrip decodes every golden file into
// its type, rejecting unknown fields, then re-encodes it and compares JSON
// values: a missing field, an extra field, or a null that comes back as ""
// or 0 all fail.
func TestGoldenFilesDecodeStrictlyAndRoundTrip(t *testing.T) {
	for name, newValue := range goldenTypes {
		t.Run(name, func(t *testing.T) {
			data := readGolden(t, name)
			v := newValue()

			require.NoError(t, decodeStrict(data, v))

			again, err := json.Marshal(v)
			require.NoError(t, err)
			require.JSONEq(t, string(data), string(again))
		})
	}
}

// TestNullableFieldsRoundTrip nulls every field the app can send as null
// (from its jbuilder views and db/schema.rb at 85a00a0) and checks each
// still round-trips. A golden file shows one value per field, so this pins
// the pointer types the golden files can't.
func TestNullableFieldsRoundTrip(t *testing.T) {
	tests := []struct {
		file  string
		paths [][]string // keys to set to null, in the first item for a list
	}{
		{"event.json", [][]string{
			{"description"}, {"internal_notes"}, {"ends_on"}, {"starts_at"}, {"ends_at"},
			{"recurrence_rule"}, {"recurrence_text"},
			{"location", "name"}, {"location", "address"}, {"location", "map_url"},
			{"location", "latitude"}, {"location", "longitude"},
			{"image"}, {"created_by"},
		}},
		{"event.json", [][]string{{"image", "alt"}}},
		{"events.json", [][]string{{"occurrence_starts_at"}, {"occurrence_ends_at"}, {"starts_at"}, {"ends_at"}}},
		{"tag.json", [][]string{{"color"}, {"position"}}},
		{"rsvps.json", [][]string{{"phone"}, {"occurrence_date"}}},
		{"access_tokens.json", [][]string{{"last_used_at"}}},
		// GET /api_token when there's no website token: all but exists.
		{"api_token.json", [][]string{{"description"}, {"last_used_at"}, {"created_at"}}},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			var doc any
			require.NoError(t, json.Unmarshal(readGolden(t, tt.file), &doc))
			target := doc
			if list, ok := doc.([]any); ok {
				target = list[0]
			}
			for _, path := range tt.paths {
				setNull(t, target, path)
			}
			nulled, err := json.Marshal(doc)
			require.NoError(t, err)

			v := goldenTypes[tt.file]()
			require.NoError(t, decodeStrict(nulled, v))
			again, err := json.Marshal(v)
			require.NoError(t, err)
			require.JSONEq(t, string(nulled), string(again))
		})
	}
}

// TestEventTagColorCanBeNull covers the nested tags' color, which the
// table above can't reach inside a list.
func TestEventTagColorCanBeNull(t *testing.T) {
	data := []byte(`{"id":"t1","name":"Worship","color":null}`)
	var got EventTag

	require.NoError(t, decodeStrict(data, &got))
	again, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, string(data), string(again))
}

// TestStrictDecodeCatchesAdditions shows the drift alarm fires for a new
// field, including one on an occurrence, whose Event is embedded.
func TestStrictDecodeCatchesAdditions(t *testing.T) {
	for _, name := range []string{"event.json", "events.json", "tag.json", "error_not_found.json"} {
		t.Run(name, func(t *testing.T) {
			var doc any
			require.NoError(t, json.Unmarshal(readGolden(t, name), &doc))
			target := doc
			if list, ok := doc.([]any); ok {
				target = list[0]
			}
			target.(map[string]any)["added_by_the_server"] = true
			data, err := json.Marshal(doc)
			require.NoError(t, err)

			err = decodeStrict(data, goldenTypes[name]())

			require.ErrorContains(t, err, `unknown field "added_by_the_server"`)
		})
	}
}

// setNull sets the value at path inside doc to null, failing if the path
// doesn't exist (so a renamed field can't make this test vacuous).
func setNull(t *testing.T, doc any, path []string) {
	t.Helper()
	obj, ok := doc.(map[string]any)
	require.True(t, ok, "not an object at %v", path)
	for _, key := range path[:len(path)-1] {
		obj, ok = obj[key].(map[string]any)
		require.True(t, ok, "no object at %q in %v", key, path)
	}
	last := path[len(path)-1]
	_, exists := obj[last]
	require.True(t, exists, "no %q in %v", last, path)
	obj[last] = nil
}
