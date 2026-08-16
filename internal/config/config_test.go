package config

import (
	"os"
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("INFLUX_URL", "http://localhost:8086")
	t.Setenv("INFLUX_TOKEN", "test-token")
	t.Setenv("INFLUX_ORG", "test-org")
	t.Setenv("INFLUX_BUCKET", "test-bucket")
	t.Setenv("DRAGONFLY_HOST", "localhost")
}

func TestNewConfigDefaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.LogLevel != "error" {
		t.Errorf("expected default log level 'error', got %q", cfg.LogLevel)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Port)
	}
	if cfg.InfluxMeasurement != "weather" {
		t.Errorf("expected default measurement 'weather', got %q", cfg.InfluxMeasurement)
	}
	if cfg.QueryTimeout != 10*time.Second {
		t.Errorf("expected default query timeout 10s, got %v", cfg.QueryTimeout)
	}
	if cfg.DragonflyPort != 6379 {
		t.Errorf("expected default dragonfly port 6379, got %d", cfg.DragonflyPort)
	}
	if cfg.DragonflyKeyPrefix != "tempest" {
		t.Errorf("expected default dragonfly key prefix 'tempest', got %q", cfg.DragonflyKeyPrefix)
	}
	if cfg.CacheResultsDuration != 5*time.Minute {
		t.Errorf("expected default cache duration 5m, got %v", cfg.CacheResultsDuration)
	}
	if cfg.AuthenticationEnabled {
		t.Error("expected authentication disabled by default")
	}
	if !cfg.MetricsEnabled {
		t.Error("expected metrics enabled by default")
	}
	if cfg.MetricsPort != 8081 {
		t.Errorf("expected default metrics port 8081, got %d", cfg.MetricsPort)
	}
	if cfg.TracingEnabled {
		t.Error("expected tracing disabled by default")
	}
	if cfg.TracingService != "tempest-influxdb-api" {
		t.Errorf("expected default tracing service 'tempest-influxdb-api', got %q", cfg.TracingService)
	}
}

func TestNewConfigOverrides(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("API_KEYS", "key-one,key-two")
	t.Setenv("AUTHENTICATION_ENABLED", "true")

	cfg, err := NewConfig()
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port)
	}
	if !cfg.AuthenticationEnabled {
		t.Error("expected authentication enabled")
	}
	if len(cfg.APIKeys) != 2 || cfg.APIKeys[0] != "key-one" || cfg.APIKeys[1] != "key-two" {
		t.Errorf("expected api keys [key-one key-two], got %v", cfg.APIKeys)
	}
}

func TestNewConfigMissingRequired(t *testing.T) {
	// t.Setenv registers cleanup so the unset below does not leak between tests.
	for _, key := range []string{"INFLUX_URL", "INFLUX_TOKEN", "INFLUX_ORG", "INFLUX_BUCKET", "DRAGONFLY_HOST"} {
		t.Setenv(key, "placeholder")
		_ = os.Unsetenv(key)
	}

	_, err := NewConfig()
	if err == nil {
		t.Fatal("expected error when required env vars are missing, got nil")
	}
}
