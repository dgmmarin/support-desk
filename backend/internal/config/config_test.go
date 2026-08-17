package config

import (
	"strings"
	"testing"
)

// test_config_missing_required_fails_fast (ISSUE-0001)
func TestConfigMissingRequiredFailsFast(t *testing.T) {
	// One required value blank, the rest present.
	t.Setenv("DATABASE_URL", "")
	t.Setenv("NATS_URL", "nats://localhost:4222")
	t.Setenv("TIKA_URL", "http://localhost:9998")
	t.Setenv("CLAMAV_ADDR", "localhost:3310")

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail when DATABASE_URL is missing")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("error should name the missing key, got: %v", err)
	}
}

func TestConfigLoadsWhenAllPresent(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5433/db")
	t.Setenv("NATS_URL", "nats://localhost:4222")
	t.Setenv("TIKA_URL", "http://localhost:9998")
	t.Setenv("CLAMAV_ADDR", "localhost:3310")

	c, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.DatabaseURL == "" || c.NATSURL == "" {
		t.Fatal("expected required fields to be populated")
	}
	if c.HTTPAddr == "" {
		t.Fatal("expected HTTPAddr to default")
	}
}
