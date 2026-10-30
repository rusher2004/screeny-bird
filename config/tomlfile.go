package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pelletier/go-toml/v2"
)

// maxTOMLBytes bounds the config file read. It is a safety invariant, not an
// operator setting.
const maxTOMLBytes = 1 << 20

// TOMLFile loads the settings from a TOML file. The file is decoded strictly:
// unknown keys, wrong types and missing or empty settings are errors.
type TOMLFile struct {
	// FS and Name locate the file: Name is a path within FS.
	FS   fs.FS
	Name string
}

// Load reads, strictly decodes and validates the file.
func (f TOMLFile) Load(_ context.Context) (Config, error) {
	info, err := fs.Stat(f.FS, f.Name)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file %q: %w", f.Name, err)
	}

	if info.Size() > maxTOMLBytes {
		return Config{}, fmt.Errorf("config file %q is %d bytes, over the %d byte limit: %w", f.Name, info.Size(), maxTOMLBytes, ErrInvalidConfig)
	}

	data, err := fs.ReadFile(f.FS, f.Name)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file %q: %w", f.Name, err)
	}

	var cfg Config

	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		if strict, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return Config{}, fmt.Errorf("config file %q has unknown settings: %w: %s", f.Name, ErrInvalidConfig, strict.String())
		}

		return Config{}, fmt.Errorf("decoding config file %q: %w: %w", f.Name, ErrInvalidConfig, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config file %q: %w", f.Name, err)
	}

	return cfg, nil
}
