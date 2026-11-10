//go:build !darwin

package netdoctor

func macEnvReach(int, string) error { return nil }
