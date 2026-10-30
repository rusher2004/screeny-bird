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

// Validate reports the first required setting that is missing or empty.
// Every loader calls it, whatever the source.
func (c Config) Validate() error {
	if strings.TrimSpace(c.CatalogDir) == "" {
		return fmt.Errorf("catalog_dir: %w", ErrMissingSetting)
	}

	return nil
}
