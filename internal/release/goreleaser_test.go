package release_test

import (
	"os"
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

	// Check EVERY archive entry (not just archives[0])
	for i, archive := range config.Archives {
		archiveFiles := archive.Files

		// Required files: exact matches only (not substring or fuzzy)
		// This prevents LICENSE.bak, MY_README, etc. from passing
		required := []string{"LICENSE", "README*", "completions/*"}

		for _, requiredFile := range required {
			found := false
			for _, file := range archiveFiles {
				if file == requiredFile {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("archives[%d]: %q missing from files. Found: %v", i, requiredFile, archiveFiles)
			}
		}
	}
}
