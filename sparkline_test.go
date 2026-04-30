package main

import (
	"strings"
	"testing"
)

func TestRenderSparklineEmpty(t *testing.T) {
	result := renderSparkline(nil, 10)
	if len([]rune(result)) != 10 {
		t.Errorf("expected 10 runes, got %d", len([]rune(result)))
	}
	for _, r := range result {
		if r != '▁' {
			t.Errorf("expected all ▁, got %c", r)
		}
	}
}

func TestRenderSparklineAllZeros(t *testing.T) {
	result := renderSparkline([]float64{0, 0, 0}, 5)
	for _, r := range result {
		if r != '▁' {
			t.Errorf("expected all ▁ for zero data, got %c", r)
		}
	}
}

func TestRenderSparklineSingleMax(t *testing.T) {
	result := renderSparkline([]float64{100}, 1)
	if result != "█" {
		t.Errorf("expected █ for single max value, got %s", result)
	}
}

func TestRenderSparklineGradient(t *testing.T) {
	samples := []float64{0, 1, 2, 3, 4, 5, 6, 7}
	result := renderSparkline(samples, 8)
	runes := []rune(result)
	if len(runes) != 8 {
		t.Fatalf("expected 8 runes, got %d", len(runes))
	}
	if runes[0] != '▁' {
		t.Errorf("first rune should be ▁, got %c", runes[0])
	}
	if runes[7] != '█' {
		t.Errorf("last rune should be █, got %c", runes[7])
	}
	for i := 1; i < len(runes); i++ {
		if runes[i] < runes[i-1] {
			t.Errorf("sparkline not monotonically increasing at position %d", i)
		}
	}
}

func TestRenderSparklineWidthTruncation(t *testing.T) {
	samples := make([]float64, 100)
	for i := range samples {
		samples[i] = float64(i)
	}
	result := renderSparkline(samples, 20)
	if len([]rune(result)) != 20 {
		t.Errorf("expected 20 runes, got %d", len([]rune(result)))
	}
}

func TestRenderSparklineZeroWidth(t *testing.T) {
	result := renderSparkline([]float64{1, 2, 3}, 0)
	if result != "" {
		t.Errorf("expected empty string for zero width, got %q", result)
	}
}

func TestAppendRing(t *testing.T) {
	buf := []float64{1, 2, 3}
	buf = appendRing(buf, 4, 3)
	if len(buf) != 3 {
		t.Fatalf("expected 3 items, got %d", len(buf))
	}
	if buf[0] != 2 || buf[1] != 3 || buf[2] != 4 {
		t.Errorf("expected [2 3 4], got %v", buf)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0 B/s"},
		{512, "512 B/s"},
		{1024, "1 KB/s"},
		{1536, "1.5 KB/s"},
		{1048576, "1 MB/s"},
		{1572864, "1.5 MB/s"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.input)
		if !strings.Contains(got, strings.Split(tt.want, " ")[1]) {
			t.Errorf("formatBytes(%v) = %q, want suffix from %q", tt.input, got, tt.want)
		}
	}
}
