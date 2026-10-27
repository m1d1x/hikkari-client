//go:build !windows && !darwin

package applist

func List(_ bool) []AppEntry { return nil }
