package main

import (
	"testing"
)

func TestEscapeAppleScript(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{`say "hello"`, `say \"hello\"`},
		{`path\to\file`, `path\\to\\file`},
		{`"quoted" and \back`, `\"quoted\" and \\back`},
		{"", ""},
	}

	for _, tt := range tests {
		got := escapeAppleScript(tt.input)
		if got != tt.expected {
			t.Errorf("escapeAppleScript(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
