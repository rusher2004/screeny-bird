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
}

// validate reports every required setting that is missing or empty, joined
// into one error. Every loader in this package calls it, whatever the source.
func (c Config) validate() error {
	var errs []error

	if strings.TrimSpace(c.CatalogDir) == "" {
		errs = append(errs, fmt.Errorf("catalog_dir: %w", ErrMissingSetting))
	}

	return errors.Join(errs...)
}
