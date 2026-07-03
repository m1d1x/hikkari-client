//go:build !windows

package applist

func List(_ bool) []AppEntry { return nil }
