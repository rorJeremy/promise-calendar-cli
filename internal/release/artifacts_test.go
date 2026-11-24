package release_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshotArtifacts validates that goreleaser snapshot builds actually
// include LICENSE, README.md, and completions in the archives.
// This complements TestGoReleaserConfig by checking real artifacts, not just YAML.
//
// Run with: TEST_SNAPSHOT=1 go test -v ./internal/release -run TestSnapshotArtifacts
// or: make test-snapshot
//
// Skip in normal `make check` to keep it fast (snapshot build takes ~3s).
// Builds into a temp directory and leaves the repo's dist/ untouched.
func TestSnapshotArtifacts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping snapshot build test in short mode")
	}

	// Check if we're in snapshot test mode
	if os.Getenv("TEST_SNAPSHOT") == "" {
		t.Skip("Skipping snapshot build test (set TEST_SNAPSHOT=1 to run)")
	}

	// Find goreleaser - must FAIL if not found (not skip)
	goreleaser, err := exec.LookPath("goreleaser")
	if err != nil {
		t.Fatal("goreleaser not found in PATH (required for artifact validation)")
	}

	// Get repo root (don't chdir to it - use cmd.Dir instead)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to get absolute path to repo root: %v", err)
	}

	// Build into a temp directory (don't touch repo's dist/)
	tempDist := t.TempDir()
	t.Logf("Building into temp dist: %s", tempDist)

	// Create a temporary config with custom dist path
	tempConfig := filepath.Join(t.TempDir(), ".goreleaser.yaml")
	origConfig, err := os.ReadFile(filepath.Join(repoRoot, ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("failed to read .goreleaser.yaml: %v", err)
	}

	// Guard: Check if .goreleaser.yaml already has a dist: key
	if strings.Contains(string(origConfig), "\ndist:") || strings.HasPrefix(string(origConfig), "dist:") {
		t.Fatal(".goreleaser.yaml already contains a dist: key - cannot override safely")
	}

	// Prepend dist setting to config
	configWithDist := fmt.Sprintf("dist: %s\n%s", tempDist, string(origConfig))
	if err := os.WriteFile(tempConfig, []byte(configWithDist), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	// Guard: Check completions/ before build (hooks may create it)
	completionsExisted := false
	if _, err := os.Stat(filepath.Join(repoRoot, "completions")); err == nil {
		completionsExisted = true
	}

	// If completions/ gets created by hooks, we'll clean it up
	defer func() {
		if !completionsExisted {
			compPath := filepath.Join(repoRoot, "completions")
			if _, err := os.Stat(compPath); err == nil {
				t.Logf("Cleaning up completions/ created by goreleaser hooks")
				_ = os.RemoveAll(compPath)
			}
		}
	}()

	// Run snapshot build (without --clean, with custom config that sets dist)
	t.Log("Running goreleaser snapshot build...")
	cmd := exec.Command(goreleaser, "release", "--snapshot", "--skip=publish", "--config", tempConfig)
	cmd.Dir = repoRoot // Run from repo root, but build into temp
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("goreleaser snapshot failed: %v\nOutput:\n%s", err, output)
	}

	// Check archives in temp dist (not repo dist/)
	distDir := tempDist
	entries, err := os.ReadDir(distDir)
	if err != nil {
		t.Fatalf("failed to read dist directory: %v", err)
	}

	// Find all archives (.tar.gz and .zip)
	archives := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".zip") {
			archives = append(archives, filepath.Join(distDir, name))
		}
	}

	if len(archives) == 0 {
		t.Fatal("no archives found in dist/")
	}

	t.Logf("Found %d archives to check", len(archives))

	// Check each archive
	for _, archivePath := range archives {
		t.Run(filepath.Base(archivePath), func(t *testing.T) {
			var files []string
			var err error

			if strings.HasSuffix(archivePath, ".tar.gz") {
				files, err = listTarGz(archivePath)
			} else if strings.HasSuffix(archivePath, ".zip") {
				files, err = listZip(archivePath)
			} else {
				t.Fatalf("unknown archive type: %s", archivePath)
			}

			if err != nil {
				t.Fatalf("failed to list archive: %v", err)
			}

			// Required files in every archive
			required := map[string]bool{
				"LICENSE":   false,
				"README.md": false,
			}
			// At least one completion file
			hasCompletion := false

			for _, file := range files {
				base := filepath.Base(file)
				if base == "LICENSE" {
					required["LICENSE"] = true
				}
				if base == "README.md" {
					required["README.md"] = true
				}
				if strings.HasPrefix(base, "pcal.") && (strings.HasSuffix(base, ".bash") ||
					strings.HasSuffix(base, ".zsh") || strings.HasSuffix(base, ".fish")) {
					hasCompletion = true
				}
			}

			// Check required files
			for file, found := range required {
				if !found {
					t.Errorf("%s missing from archive. Files: %v", file, files)
				}
			}

			// Check completions
			if !hasCompletion {
				t.Errorf("no completion files (pcal.bash/zsh/fish) found in archive. Files: %v", files)
			}
		})
	}
}

func listTarGz(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	var files []string

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg {
			files = append(files, header.Name)
		}
	}

	return files, nil
}

func listZip(path string) ([]string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	var files []string
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			files = append(files, f.Name)
		}
	}

	return files, nil
}
