// Package config defines the service's settings and the loaders that supply
// them. Every setting is required and there are no defaults.
package config

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrMissingSetting reports a required setting that is absent or empty.
	ErrMissingSetting = errors.New("missing required setting")
	// ErrInvalidConfig reports a source that is not valid, strictly decodable
	// configuration for the settings this program defines.
	ErrInvalidConfig = errors.New("invalid config")
)

// Config holds every setting the service needs.
type Config struct {
	// CatalogDir is the directory holding the catalog's index.json and species/.
	CatalogDir string `toml:"catalog_dir"`
	// WorkDir is scratch space for catalog sync. It must be on the same
	// filesystem as CatalogDir so publishing a species is an atomic rename.
	WorkDir string `toml:"work_dir"`
	// UpstreamRepo is the GitHub repository ("owner/name") the catalog syncs from.
	UpstreamRepo string `toml:"upstream_repo"`
	// UpstreamRef is the branch or tag of UpstreamRepo that sync follows.
	UpstreamRef string `toml:"upstream_ref"`
	// UpstreamTimeoutSeconds bounds each request to the upstream repository.
	UpstreamTimeoutSeconds int `toml:"upstream_timeout_seconds"`
}

// validate reports every required setting that is missing, empty or unusable,
// joined into one error. Every loader in this package calls it, whatever the
// source.
func (c Config) validate() error {
	var errs []error

	for _, setting := range []struct {
		name  string
		value string
	}{
		{"catalog_dir", c.CatalogDir},
		{"work_dir", c.WorkDir},
		{"upstream_repo", c.UpstreamRepo},
		{"upstream_ref", c.UpstreamRef},
	} {
		if strings.TrimSpace(setting.value) == "" {
			errs = append(errs, fmt.Errorf("%s: %w", setting.name, ErrMissingSetting))
		}
	}

	switch {
	case c.UpstreamTimeoutSeconds == 0:
		errs = append(errs, fmt.Errorf("upstream_timeout_seconds: %w", ErrMissingSetting))
	case c.UpstreamTimeoutSeconds < 0:
		errs = append(errs, fmt.Errorf("upstream_timeout_seconds: must be positive, got %d: %w", c.UpstreamTimeoutSeconds, ErrInvalidConfig))
	}

	return errors.Join(errs...)
}
