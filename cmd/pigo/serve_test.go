package main

import (
	"testing"
)

func TestParseServeArgs(t *testing.T) {
	opts, err := parseServeArgs([]string{"--port", "5000", "--hostname", "0.0.0.0", "--password", "x", "--cors", "http://a", "--cors", "http://b"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.port != 5000 || opts.hostname != "0.0.0.0" || opts.password != "x" {
		t.Fatalf("opts = %+v", opts)
	}
	if len(opts.cors) != 2 || opts.cors[0] != "http://a" || opts.cors[1] != "http://b" {
		t.Fatalf("cors = %v", opts.cors)
	}
}

func TestParseServeArgsRejectsPositional(t *testing.T) {
	if _, err := parseServeArgs([]string{"extra"}); err == nil {
		t.Fatal("expected error for positional argument")
	}
}

func TestIsLoopback(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
		if !isLoopback(host) {
			t.Errorf("%q should be loopback", host)
		}
	}
	for _, host := range []string{"0.0.0.0", "192.168.1.2"} {
		if isLoopback(host) {
			t.Errorf("%q should not be loopback", host)
		}
	}
}
