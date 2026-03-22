package session

import (
	"crypto/tls"
	"testing"
)

func TestRedisOptionsFromURLRedissEnforcesTLS(t *testing.T) {
	opts, err := RedisOptionsFromURL("rediss://:pass@localhost:6380/0")
	if err != nil {
		t.Fatalf("RedisOptionsFromURL: %v", err)
	}
	if opts.TLSConfig == nil {
		t.Fatal("expected TLS config for rediss URL")
	}
	if opts.TLSConfig.MinVersion != 0 && opts.TLSConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("expected TLS min version >= 1.2, got %d", opts.TLSConfig.MinVersion)
	}
}

func TestRedisOptionsFromURLRedisKeepsTLSNil(t *testing.T) {
	opts, err := RedisOptionsFromURL("redis://:pass@localhost:6379/0")
	if err != nil {
		t.Fatalf("RedisOptionsFromURL: %v", err)
	}
	if opts.TLSConfig != nil {
		t.Fatalf("expected nil TLS config for redis URL, got %#v", opts.TLSConfig)
	}
}
