package main

import (
	"log/slog"
	"testing"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/config"
	"github.com/rusher2004/screeny-bird/derive"
)

func TestNewServerAndProberBuildFromAValidConfig(t *testing.T) {
	t.Parallel()

	cfg := validConfig(t.TempDir())

	loader, err := catalog.NewDirLoader(cfg.CatalogDir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	derived, err := derive.NewDirLoader(cfg.DerivedDir)
	if err != nil {
		t.Fatalf("derive.NewDirLoader() error = %v", err)
	}

	if _, err := newServer(cfg, "1.2.3", loader, derived, slog.New(slog.DiscardHandler)); err != nil {
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

	derived, err := derive.NewDirLoader(cfg.DerivedDir)
	if err != nil {
		t.Fatalf("derive.NewDirLoader() error = %v", err)
	}

	if _, err := newServer(cfg, "1.2.3", loader, derived, slog.New(slog.DiscardHandler)); err == nil {
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
