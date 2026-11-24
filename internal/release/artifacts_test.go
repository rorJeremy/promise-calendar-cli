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

// captureGitStatus returns "git status --porcelain" output for repo state checking
func captureGitStatus(t *testing.T, repoRoot string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("git status failed (non-git repo?): %v", err)
		return ""
	}
	return string(output)
}

// TestSnapshotArtifacts validates that goreleaser snapshot builds actually
// include LICENSE, README.md, and completions in the archives.
// This complements TestGoReleaserConfig by checking real artifacts, not just YAML.
//
// Run with: TEST_SNAPSHOT=1 go test -v ./internal/release -run TestSnapshotArtifacts
// or: make test-snapshot
//
// NOT part of `make check` (kept separate to keep CI fast - snapshot build takes ~3s).
// Explicitly run via `make test-snapshot` or in release.yml before publishing.
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

	// Guard: Capture repo tree state before build (hooks may modify files)
	// goreleaser before.hooks includes "go mod tidy" which can modify go.mod/go.sum
	gitStatusBefore := captureGitStatus(t, repoRoot)
	defer func() {
		gitStatusAfter := captureGitStatus(t, repoRoot)
		if gitStatusBefore != gitStatusAfter {
			t.Errorf("repo tree modified by build:\nBefore:\n%s\nAfter:\n%s",
				gitStatusBefore, gitStatusAfter)
		}
	}()

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
	archivePattern := filepath.Join(tempDist, "*.{tar.gz,zip}")
	t.Logf("Looking for archives matching: %s", archivePattern)

	// Find all archives (goreleaser creates one per platform)
	archives := []string{}
	for _, ext := range []string{".tar.gz", ".zip"} {
		matches, err := filepath.Glob(filepath.Join(tempDist, "*"+ext))
		if err != nil {
			t.Fatalf("failed to glob archives: %v", err)
		}
		archives = append(archives, matches...)
	}

	if len(archives) == 0 {
		t.Fatal("no archives found in temp dist")
	}

	t.Logf("Found %d archives to check", len(archives))

	// Check each archive
	for _, archivePath := range archives {
		archiveName := filepath.Base(archivePath)
		t.Run(archiveName, func(t *testing.T) {
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

			// Check required files
			hasLicense := false
			hasReadme := false
			hasCompletions := false

			for _, file := range files {
				name := filepath.Base(file)
				if name == "LICENSE" {
					hasLicense = true
				}
				if name == "README.md" {
					hasReadme = true
				}
				if strings.HasPrefix(name, "pcal.") &&
					(strings.HasSuffix(name, ".bash") ||
						strings.HasSuffix(name, ".zsh") ||
						strings.HasSuffix(name, ".fish")) {
					hasCompletions = true
				}
			}

			if !hasLicense {
				t.Errorf("missing required file: LICENSE. Files: %v", files)
			}
			if !hasReadme {
				t.Errorf("missing required file: README.md. Files: %v", files)
			}
			if !hasCompletions {
				t.Errorf("no completion files (pcal.bash/zsh/fish) found in archive. Files: %v", files)
			}
		})
	}
}

// listTarGz returns all file paths in a tar.gz archive
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

// listZip returns all file paths in a zip archive
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
