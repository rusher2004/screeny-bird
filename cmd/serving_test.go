package main

import (
	"log/slog"
	"testing"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/config"
)

func TestHealthURLTargetsTheServerFromInsideTheSameHost(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		listen  string
		want    string
		wantErr bool
	}{
		"all interfaces, no host":     {listen: ":8080", want: "http://127.0.0.1:8080/healthz"},
		"all interfaces, IPv4":        {listen: "0.0.0.0:9000", want: "http://127.0.0.1:9000/healthz"},
		"all interfaces, IPv6":        {listen: "[::]:9000", want: "http://127.0.0.1:9000/healthz"},
		"loopback":                    {listen: "127.0.0.1:8080", want: "http://127.0.0.1:8080/healthz"},
		"a specific address":          {listen: "10.1.2.3:8080", want: "http://10.1.2.3:8080/healthz"},
		"a specific IPv6 address":     {listen: "[fd00::1]:8080", want: "http://[fd00::1]:8080/healthz"},
		"a host name":                 {listen: "screeny.internal:8080", want: "http://screeny.internal:8080/healthz"},
		"no port":                     {listen: "localhost", wantErr: true},
		"empty":                       {listen: "", wantErr: true},
		"too many colons, no bracket": {listen: "::1:8080", wantErr: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := healthURL(tt.listen)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("healthURL(%q) = %q, %v; want %q (error %t)", tt.listen, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestNewServerAndProberBuildFromAValidConfig(t *testing.T) {
	t.Parallel()

	cfg := validConfig(t.TempDir())

	loader, err := catalog.NewDirLoader(cfg.CatalogDir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	if _, err := newServer(cfg, "1.2.3", loader, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("newServer() error = %v", err)
	}

	if _, err := newProber(cfg); err != nil {
		t.Fatalf("newProber() error = %v", err)
	}
}

func TestNewServerRejectsUnusableSettings(t *testing.T) {
	t.Parallel()

	cfg := validConfig(t.TempDir())
	cfg.CORSAllowedOrigins = []string{"*"}
	cfg.ListenAddress = " "

	loader, err := catalog.NewDirLoader(cfg.CatalogDir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	if _, err := newServer(cfg, "1.2.3", loader, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("newServer() with a blank address and a wildcard origin error = nil, want failure")
	}
}

func TestNewProberRejectsAnUnusableAddress(t *testing.T) {
	t.Parallel()

	cfg := config.Config{ListenAddress: "no-port", HealthcheckTimeoutSeconds: 5}

	if _, err := newProber(cfg); err == nil {
		t.Fatal("newProber() with an address lacking a port error = nil, want failure")
	}
}
