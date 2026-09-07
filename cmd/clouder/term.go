package main

import (
	"os"
	"strconv"
)

// termWidth is the terminal width for the diff renderer. It reads COLUMNS and
// falls back to a sane default; a real ioctl-based probe can replace this
// without touching the renderer, which already takes width as a parameter.
func termWidth() int {
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 20 {
			return n
		}
	}
	return 100
}

// isTTY reports whether stdout is a terminal, so ANSI colour is only emitted
// when something can render it.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
