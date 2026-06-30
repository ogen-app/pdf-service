// Package config loads pdf-service settings from the environment via envconfig.
package config

import (
	"strings"

	"github.com/kelseyhightower/envconfig"
)

// Config holds the service settings. Read from the environment with no global
// prefix (each field names its own var), matching the Ogen API's config style.
type Config struct {
	// Listen is the gRPC listen address. Accepts a full "host:port" or a bare
	// port (e.g. "50051", as Railway's PORT provides) — Load normalises a bare
	// port to ":port" so net.Listen gets the leading colon it requires.
	Listen string `envconfig:"PDF_SERVICE_LISTEN" default:":50051"`
	// Workers is advisory: the single-threaded pdfium backend serialises work
	// regardless (see pdfengine.Config.Workers).
	Workers int `envconfig:"PDF_SERVICE_WORKERS" default:"4"`
}

// Load reads and validates the configuration from the environment.
func Load() (*Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return nil, err
	}
	// A bare port like "50051" is a valid env value (Railway's PORT) but net.Listen
	// needs "host:port" — without a colon it errors "missing port in address".
	// Prefix it so the service binds all interfaces on that port.
	if c.Listen != "" && !strings.Contains(c.Listen, ":") {
		c.Listen = ":" + c.Listen
	}
	return &c, nil
}
