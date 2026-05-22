//go:build unix

package main

import "syscall"

// pidAlive reports whether a process with the given pid currently exists.
// Signal 0 performs existence checking without delivering a signal.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
