//go:build !windows

package main

import "os"

// replaceFile atomically replaces to with from. POSIX rename never fails
// because a reader has the target open; see rename_windows.go for Windows.
func replaceFile(from, to string) error { return os.Rename(from, to) }
