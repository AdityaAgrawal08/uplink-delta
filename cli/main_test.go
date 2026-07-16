package main

import (
	"regexp"
	"testing"
)

func TestGenerateShareCode(t *testing.T) {
	// Generate 100 codes and verify structure and uniqueness
	codes := make(map[string]bool)
	alphanumeric := regexp.MustCompile("^[a-zA-Z0-9]{6}$")

	for i := 0; i < 100; i++ {
		code := generateShareCode()
		if len(code) != 6 {
			t.Errorf("expected code length 6, got %d", len(code))
		}
		if !alphanumeric.MatchString(code) {
			t.Errorf("code %q contains non-alphanumeric characters", code)
		}
		if codes[code] {
			t.Errorf("duplicate code generated: %q", code)
		}
		codes[code] = true
	}
}

func TestShouldSkip(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"hidden file", ".gitignore", true},
		{"temp file", "file.tmp", true},
		{"swap file", "file.swp", true},
		{"regular file", "document.pdf", false},
		{"nested regular file", "path/to/image.png", false},
		{"nested hidden file", "path/to/.config", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkip(tt.input)
			if got != tt.expected {
				t.Errorf("shouldSkip(%q) = %v; expected %v", tt.input, got, tt.expected)
			}
		})
	}
}
