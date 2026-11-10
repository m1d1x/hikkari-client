//go:build darwin

package netdoctor

func macEnvReach(idx int, target string) error { return macEnv{}.reach(idx, target) }
