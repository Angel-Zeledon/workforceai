//go:build !linux

package runnerd

// limitChild: resource limits are applied on Linux only (the production
// container); elsewhere the wall-clock limit and the output caps still hold.
func limitChild(pid, memMB int) error { return nil }
