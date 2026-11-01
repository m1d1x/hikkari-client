package netdoctor

import "testing"

func TestFilterHostsDisablesOnlyProtectedOverrides(t *testing.T) {
	in := "# Copyright (c) Microsoft\r\n" +
		"127.0.0.1       localhost\r\n" +
		"0.0.0.0 store.steampowered.com\r\n" +
		"1.2.3.4 cdn.discordapp.com other.example # old mirror\r\n" +
		"0.0.0.0 ads.example.com\r\n" +
		"# 0.0.0.0 youtube.com\r\n" +
		"0.0.0.0 notsteampowered.com\r\n" +
		"10.0.0.1 hikkari.net"
	want := "# Copyright (c) Microsoft\r\n" +
		"127.0.0.1       localhost\r\n" +
		HostsMark + "0.0.0.0 store.steampowered.com\r\n" +
		HostsMark + "1.2.3.4 cdn.discordapp.com other.example # old mirror\r\n" +
		"0.0.0.0 ads.example.com\r\n" +
		"# 0.0.0.0 youtube.com\r\n" +
		"0.0.0.0 notsteampowered.com\r\n" +
		HostsMark + "10.0.0.1 hikkari.net"
	out, n := FilterHosts([]byte(in))
	if string(out) != want || n != 3 {
		t.Fatalf("n=%d\n%s", n, out)
	}
	// Idempotent: a second pass finds nothing.
	if again, n2 := FilterHosts(out); n2 != 0 || string(again) != want {
		t.Fatalf("second pass changed %d lines", n2)
	}
}
