//go:build windows

package main

import "os"

// pidAlive reports whether a process with the given pid currently exists.
// On Windows os.FindProcess opens the process and fails when it is gone.
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
