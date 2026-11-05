//go:build !windows

package main

// Console builds already print fatal errors to stderr (and the log file).
func notifyFatal(string) {}
