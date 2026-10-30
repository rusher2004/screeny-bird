package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// maxTOMLBytes bounds the config file read. It is a safety invariant, not an
// operator setting.
const maxTOMLBytes = 1 << 20

// TOMLLoader loads the settings from TOML files on disk. Files are decoded
// strictly: unknown keys, wrong types and missing or empty settings are errors.
type TOMLLoader struct{}

// LoadConfig reads, strictly decodes and validates the file at path.
func (TOMLLoader) LoadConfig(path string) (Config, error) {
	fsys, name := os.DirFS(filepath.Dir(path)), filepath.Base(path)

	info, err := fs.Stat(fsys, name)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file %q: %w", path, err)
	}

	if info.Size() > maxTOMLBytes {
		return Config{}, fmt.Errorf("config file %q is %d bytes, over the %d byte limit: %w", path, info.Size(), maxTOMLBytes, ErrInvalidConfig)
	}

	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file %q: %w", path, err)
	}

	var cfg Config

	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		if strict, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return Config{}, fmt.Errorf("config file %q has unknown settings: %w: %s", path, ErrInvalidConfig, strict.String())
		}

		return Config{}, fmt.Errorf("decoding config file %q: %w: %w", path, ErrInvalidConfig, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config file %q: %w", path, err)
	}

	return cfg, nil
}
