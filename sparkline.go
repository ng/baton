package main

import "strings"

var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

func renderSparkline(samples []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(samples) == 0 {
		return strings.Repeat(string(sparkBlocks[0]), width)
	}

	visible := samples
	if len(visible) > width {
		visible = visible[len(visible)-width:]
	}

	max := 0.0
	for _, v := range visible {
		if v > max {
			max = v
		}
	}

	var b strings.Builder
	for _, v := range visible {
		if max == 0 {
			b.WriteRune(sparkBlocks[0])
		} else {
			idx := int((v / max) * float64(len(sparkBlocks)-1))
			if idx >= len(sparkBlocks) {
				idx = len(sparkBlocks) - 1
			}
			b.WriteRune(sparkBlocks[idx])
		}
	}

	for i := len(visible); i < width; i++ {
		b.WriteRune(sparkBlocks[0])
	}
	return b.String()
}

func appendRing(buf []float64, val float64, maxLen int) []float64 {
	buf = append(buf, val)
	if len(buf) > maxLen {
		buf = buf[len(buf)-maxLen:]
	}
	return buf
}

func formatBytes(bytesPerSec float64) string {
	switch {
	case bytesPerSec >= 1024*1024:
		return strings.TrimRight(strings.TrimRight(
			formatFloat(bytesPerSec/(1024*1024)), "0"), ".") + " MB/s"
	case bytesPerSec >= 1024:
		return strings.TrimRight(strings.TrimRight(
			formatFloat(bytesPerSec/1024), "0"), ".") + " KB/s"
	default:
		return strings.TrimRight(strings.TrimRight(
			formatFloat(bytesPerSec), "0"), ".") + " B/s"
	}
}

func formatFloat(f float64) string {
	s := strings.Builder{}
	v := int(f * 10)
	s.WriteString(itoa(v / 10))
	s.WriteByte('.')
	s.WriteString(itoa(v % 10))
	return s.String()
}

func itoa(i int) string {
	if i < 0 {
		i = -i
	}
	if i == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for i > 0 {
		buf = append(buf, byte('0'+i%10))
		i /= 10
	}
	for l, r := 0, len(buf)-1; l < r; l, r = l+1, r-1 {
		buf[l], buf[r] = buf[r], buf[l]
	}
	return string(buf)
}
