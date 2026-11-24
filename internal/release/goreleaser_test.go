package release_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGoReleaserConfig validates that the .goreleaser.yaml configuration
// includes required files in release archives.
func TestGoReleaserConfig(t *testing.T) {
	data, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatalf("failed to read .goreleaser.yaml: %v", err)
	}

	var config struct {
		Archives []struct {
			Files []string `yaml:"files"`
		} `yaml:"archives"`
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("failed to parse .goreleaser.yaml: %v", err)
	}

	if len(config.Archives) == 0 {
		t.Fatal(".goreleaser.yaml has no archives configuration")
	}

	// Check that LICENSE is in the files list
	archiveFiles := config.Archives[0].Files
	hasLicense := false
	for _, file := range archiveFiles {
		if file == "LICENSE" || strings.Contains(file, "LICENSE") {
			hasLicense = true
			break
		}
	}

	if !hasLicense {
		t.Errorf("LICENSE missing from .goreleaser.yaml archives.files. Found: %v", archiveFiles)
	}

	// Also check for README
	hasReadme := false
	for _, file := range archiveFiles {
		if strings.Contains(file, "README") {
			hasReadme = true
			break
		}
	}

	if !hasReadme {
		t.Errorf("README missing from .goreleaser.yaml archives.files. Found: %v", archiveFiles)
	}

	// Check for completions
	hasCompletions := false
	for _, file := range archiveFiles {
		if strings.Contains(file, "completions") {
			hasCompletions = true
			break
		}
	}

	if !hasCompletions {
		t.Errorf("completions missing from .goreleaser.yaml archives.files. Found: %v", archiveFiles)
	}
}
