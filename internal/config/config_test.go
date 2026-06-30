package config

import (
	"os"
	"testing"
)

func TestLoadListenNormalization(t *testing.T) {
	cases := []struct {
		name  string
		set   bool   // false -> unset (exercise the struct default)
		value string // value for PDF_SERVICE_LISTEN when set
		want  string
	}{
		{"default (unset)", false, "", ":50051"},
		{"bare port (Railway PORT)", true, "50051", ":50051"},
		{"already has colon", true, ":50051", ":50051"},
		{"full host:port", true, "0.0.0.0:50051", "0.0.0.0:50051"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("PDF_SERVICE_LISTEN", tc.value)
			} else {
				// envconfig only applies the default when the var is absent, not
				// when it is present-but-empty — so unset it.
				os.Unsetenv("PDF_SERVICE_LISTEN")
			}
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Listen != tc.want {
				t.Fatalf("Listen = %q, want %q", cfg.Listen, tc.want)
			}
		})
	}
}
