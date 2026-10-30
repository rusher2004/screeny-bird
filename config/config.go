// Package config loads the service's settings from a TOML file.
//
// Every setting is required and there are no defaults. The file is decoded
// strictly: unknown keys, wrong types and missing or empty values are errors.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// maxFileBytes bounds the config file read. It is a safety invariant, not an
// operator setting.
const maxFileBytes = 1 << 20

var (
	// ErrMissingSetting reports a required setting that is absent or empty.
	ErrMissingSetting = errors.New("missing required setting")
	// ErrInvalidConfig reports a file that is not valid, strictly decodable TOML
	// for the settings this program defines.
	ErrInvalidConfig = errors.New("invalid config file")
)

// Config holds every setting the service needs.
type Config struct {
	// CatalogDir is the directory holding the catalog's index.json and species/.
	CatalogDir string `toml:"catalog_dir"`
}

// Load reads and validates the config file name from fsys.
func Load(fsys fs.FS, name string) (Config, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file %q: %w", name, err)
	}

	if info.Size() > maxFileBytes {
		return Config{}, fmt.Errorf("config file %q is %d bytes, over the %d byte limit: %w", name, info.Size(), maxFileBytes, ErrInvalidConfig)
	}

	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file %q: %w", name, err)
	}

	var cfg Config

	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		if strict, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return Config{}, fmt.Errorf("config file %q has unknown settings: %w: %s", name, ErrInvalidConfig, strict.String())
		}

		return Config{}, fmt.Errorf("decoding config file %q: %w: %w", name, ErrInvalidConfig, err)
	}

	if strings.TrimSpace(cfg.CatalogDir) == "" {
		return Config{}, fmt.Errorf("config file %q: catalog_dir: %w", name, ErrMissingSetting)
	}

	return cfg, nil
}
