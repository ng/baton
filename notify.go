package main

import (
	"os/exec"
)

func Notify(title, message string) error {
	script := `display notification "` + escapeAppleScript(message) + `" with title "` + escapeAppleScript(title) + `"`
	return exec.Command("osascript", "-e", script).Run()
}

func escapeAppleScript(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			result = append(result, '\\', '"')
		case '\\':
			result = append(result, '\\', '\\')
		default:
			result = append(result, s[i])
		}
	}
	return string(result)
}
