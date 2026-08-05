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
	// GCPercent sets the GC target (Go's GOGC) via debug.SetGCPercent at boot. A
	// lower value collects more often, trading CPU for a smaller Go-heap
	// high-water mark. <=0 leaves the runtime default (100) in place. Governs the
	// Go heap only — not pdfium's in-process CGO allocations.
	GCPercent int `envconfig:"PDF_SERVICE_GC_PERCENT" default:"50"`
	// MemoryLimitRatio sets Go's soft memory limit (GOMEMLIMIT) to this fraction
	// of the container's cgroup memory limit, read at boot. It makes the GC lean
	// harder as the heap nears the cap, keeping RSS down and guarding against OOM
	// under a burst. Ignored when GOMEMLIMIT is set explicitly, when <=0, or when
	// no finite cgroup limit is found (e.g. local dev). GOMEMLIMIT bounds only the
	// Go heap, so leave headroom for pdfium's native (CGO) working set.
	MemoryLimitRatio float64 `envconfig:"PDF_SERVICE_MEMORY_LIMIT_RATIO" default:"0.9"`
	// ScavengeOnIdle returns freed memory to the OS (debug.FreeOSMemory) once all
	// in-flight Extract/Render calls drain to idle after a burst, so container RSS
	// tracks real usage instead of holding a high-water mark. Runs off the request
	// path; disable if the extra GC churn is unwanted. Reclaims Go-heap pages (the
	// reassembled PDF bytes, extracted text, thumbnail) — not pdfium's CGO memory.
	ScavengeOnIdle bool `envconfig:"PDF_SERVICE_SCAVENGE_ON_IDLE" default:"true"`
	// LogLevel is the minimum slog level: debug|info|warn|error. Unknown/empty
	// falls back to info. Bare LOG_LEVEL (not prefixed) matches the Ogen API's
	// knob so operators use identical settings across services (CON-107).
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`
	// LogFormat selects the slog handler: json (default, prod) or text (local).
	LogFormat string `envconfig:"LOG_FORMAT" default:"json"`
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
