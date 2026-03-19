package config

import "time"

type Config struct {
	Addr            string        // listen address, e.g. ":8080"
	DBPath          string        // SQLite database path
	PingInterval    time.Duration // heartbeat ping interval
	PongTimeout     time.Duration // max wait for pong
	MaxMissedPongs  int           // consecutive misses before disconnect
	RequestTimeout  time.Duration // max time to wait for agent response
	GracePeriod     time.Duration // name reservation after disconnect
	MaxMessageBytes int64         // max WebSocket message size
}

func Default() *Config {
	return &Config{
		Addr:            ":8080",
		DBPath:          "relay.db",
		PingInterval:    30 * time.Second,
		PongTimeout:     30 * time.Second,
		MaxMissedPongs:  3,
		RequestTimeout:  5 * time.Minute,
		GracePeriod:     60 * time.Second,
		MaxMessageBytes: 10 * 1024 * 1024, // 10MB
	}
}
