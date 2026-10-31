package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// maxTOMLBytes bounds the config file read. It is a safety invariant, not an
// operator setting.
const maxTOMLBytes = 1 << 20

// TOMLLoader loads the settings from the TOML file at one path. The file is
// decoded strictly: unknown keys, wrong types and missing or empty settings are
// errors.
type TOMLLoader struct {
	path string
}

// NewTOMLLoader returns a loader for the file at path. A blank path is an error.
func NewTOMLLoader(path string) (TOMLLoader, error) {
	if strings.TrimSpace(path) == "" {
		return TOMLLoader{}, fmt.Errorf("creating TOML loader: path: %w", ErrMissingSetting)
	}

	return TOMLLoader{path: path}, nil
}

// Load reads, strictly decodes and validates the loader's file.
func (l TOMLLoader) Load() (Config, error) {
	path := l.path
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
