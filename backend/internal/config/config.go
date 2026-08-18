// Package config loads runtime configuration from the environment.
// No secrets live in code; everything comes from the process env (.env in dev).
package config

import (
	"fmt"
	"os"
	"strings"
)

// Config is the backend's runtime configuration. All values originate from env.
type Config struct {
	DatabaseURL    string // Postgres DSN — superuser/migration role (ParadeDB)
	AppDatabaseURL string // Postgres DSN — non-superuser app role (RLS-bound); optional
	NATSURL        string // NATS/JetStream URL
	TikaURL        string // Apache Tika base URL
	ClamAVAddr     string // ClamAV daemon host:port
	HTTPAddr       string // listen address for the health/API server

	// SSO/OIDC for the RBAC guard (M11 FR-M11-04). Optional: when unset the privileged
	// planes fail closed (every request 401) rather than opening — an unconfigured IdP
	// denies privileged access, never bypasses it (SEC-05). SSOHMACSecret enables an
	// HS256 dev verifier; production wires an RS256 JWKS source (ADR-0015 / ISSUE-0064).
	SSOIssuer     string
	SSOAudience   string
	SSOHMACSecret string
}

// Load reads configuration from the environment and fails fast when a required
// value is missing, naming every missing key. This is the boot-time guard that
// keeps a half-configured process from limping along (NFR-R-01: observable boot).
func Load() (Config, error) {
	c := Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		AppDatabaseURL: os.Getenv("APP_DATABASE_URL"), // optional; falls back to DatabaseURL below
		NATSURL:        os.Getenv("NATS_URL"),
		TikaURL:        os.Getenv("TIKA_URL"),
		ClamAVAddr:     os.Getenv("CLAMAV_ADDR"),
		HTTPAddr:       getenv("HTTP_ADDR", ":8080"),
		SSOIssuer:      os.Getenv("SSO_ISSUER"),
		SSOAudience:    os.Getenv("SSO_AUDIENCE"),
		SSOHMACSecret:  os.Getenv("SSO_HMAC_SECRET"),
	}
	if c.AppDatabaseURL == "" {
		c.AppDatabaseURL = c.DatabaseURL
	}

	var missing []string
	for _, r := range []struct{ key, val string }{
		{"DATABASE_URL", c.DatabaseURL},
		{"NATS_URL", c.NATSURL},
		{"TIKA_URL", c.TikaURL},
		{"CLAMAV_ADDR", c.ClamAVAddr},
	} {
		if strings.TrimSpace(r.val) == "" {
			missing = append(missing, r.key)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
