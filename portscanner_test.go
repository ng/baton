package main

import (
	"testing"
)

func TestPortRegex(t *testing.T) {
	tests := []struct {
		line     string
		expected []string
	}{
		{
			line:     "LISTEN 0      128          0.0.0.0:3000       0.0.0.0:*",
			expected: []string{"3000"},
		},
		{
			line:     "LISTEN 0      128             [::]:8080          [::]:*",
			expected: []string{"8080"},
		},
		{
			line:     "tcp   0      0 0.0.0.0:22    0.0.0.0:*    LISTEN",
			expected: []string{"22"},
		},
		{
			line:     "no ports here",
			expected: nil,
		},
	}

	for _, tt := range tests {
		matches := portRe.FindAllStringSubmatch(tt.line, -1)
		var got []string
		for _, m := range matches {
			got = append(got, m[1])
		}
		if len(got) == 0 && len(tt.expected) == 0 {
			continue
		}
		if len(got) != len(tt.expected) {
			t.Errorf("line %q: expected %v matches, got %v", tt.line, tt.expected, got)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("line %q: match %d: expected %s, got %s", tt.line, i, tt.expected[i], got[i])
			}
		}
	}
}

func TestPortScannerExclude(t *testing.T) {
	cfg := DefaultConfig()
	conn := &Connection{cfg: cfg, host: "test"}

	ps := NewPortScanner(cfg, conn)

	if !ps.excluded[22] {
		t.Error("port 22 should be excluded")
	}
	if !ps.excluded[19222] {
		t.Error("port 19222 should be excluded")
	}
	if ps.excluded[3000] {
		t.Error("port 3000 should not be excluded")
	}
}
