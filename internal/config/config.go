package config

import (
	"fmt"
	"os"
)

type Config struct {
	GRPCAddr    string
	HTTPAddr    string
	PostgresDSN string
	RedisAddr   string
	RedisDB     string
	RedisPass   string
}

func Load() (Config, error) {
	cfg := Config{
		GRPCAddr:    getEnv("GRPC_ADDR", ":8080"),
		HTTPAddr:    getEnv("HTTP_ADDR", ":8090"),
		PostgresDSN: os.Getenv("POSTGRES_DSN"),
		RedisAddr:   getEnv("REDIS_ADDR", "localhost:6379"),
		RedisDB:     getEnv("REDIS_DB", "0"),
		RedisPass:   os.Getenv("REDIS_PASSWORD"),
	}

	if cfg.PostgresDSN == "" {
		return Config{}, fmt.Errorf("POSTGRES_DSN is required")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
