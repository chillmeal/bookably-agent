package session

import (
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

func RedisOptionsFromURL(redisURL string) (*redis.Options, error) {
	value := strings.TrimSpace(redisURL)
	if value == "" {
		return nil, fmt.Errorf("session redis: url is required")
	}

	opts, err := redis.ParseURL(value)
	if err != nil {
		return nil, fmt.Errorf("session redis: parse url: %w", err)
	}

	if strings.HasPrefix(strings.ToLower(value), "rediss://") {
		if opts.TLSConfig == nil {
			opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		} else if opts.TLSConfig.MinVersion < tls.VersionTLS12 {
			copied := opts.TLSConfig.Clone()
			copied.MinVersion = tls.VersionTLS12
			opts.TLSConfig = copied
		}
	}

	return opts, nil
}

func NewRedisClientFromURL(redisURL string) (*redis.Client, error) {
	opts, err := RedisOptionsFromURL(redisURL)
	if err != nil {
		return nil, err
	}
	return redis.NewClient(opts), nil
}
