package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rusher2004/screeny-bird/config"
)

func load(t *testing.T, content string) (config.Config, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "screeny.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	loader, err := config.NewTOMLLoader(path)
	if err != nil {
		t.Fatalf("NewTOMLLoader() error = %v", err)
	}

	return loader.Load()
}

func TestLoadReadsEverySetting(t *testing.T) {
	t.Parallel()

	got, err := load(t, "# the catalog\ncatalog_dir = \"/data/catalog\"\n")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.CatalogDir != "/data/catalog" {
		t.Fatalf("Load().CatalogDir = %q, want /data/catalog", got.CatalogDir)
	}
}

func TestLoadRequiresEverySetting(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty file":       "",
		"key absent":       "# nothing here\n",
		"empty value":      "catalog_dir = \"\"\n",
		"whitespace value": "catalog_dir = \"   \"\n",
	}

	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := load(t, content)
			if !errors.Is(err, config.ErrMissingSetting) {
				t.Fatalf("Load() error = %v, want ErrMissingSetting", err)
			}

			if !strings.Contains(err.Error(), "catalog_dir") {
				t.Errorf("error %q does not name the setting", err)
			}
		})
	}
}

func TestLoadRejectsInvalidFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unknown key":      "catalog_dir = \"/data\"\nlisten = \":8080\"\n",
		"malformed TOML":   "catalog_dir = \n",
		"wrong value type": "catalog_dir = 5\n",
		"duplicate key":    "catalog_dir = \"/a\"\ncatalog_dir = \"/b\"\n",
	}

	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := load(t, content)
			if !errors.Is(err, config.ErrInvalidConfig) {
				t.Fatalf("Load() error = %v, want ErrInvalidConfig", err)
			}

			if !strings.Contains(err.Error(), "screeny.toml") {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}
}

func TestLoadNamesTheUnknownKey(t *testing.T) {
	t.Parallel()

	_, err := load(t, "catalog_dir = \"/data\"\nlisten = \":8080\"\n")
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("Load() error = %v, want it to name the unknown key listen", err)
	}
}

func TestLoadReportsAMissingFile(t *testing.T) {
	t.Parallel()

	loader, err := config.NewTOMLLoader(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("NewTOMLLoader() error = %v", err)
	}

	_, err = loader.Load()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load() error = %v, want fs.ErrNotExist", err)
	}

	if !strings.Contains(err.Error(), "absent.toml") {
		t.Errorf("error %q does not name the file", err)
	}
}

// TestExampleConfigLoads keeps config.example.toml in step with the settings.
func TestExampleConfigLoads(t *testing.T) {
	t.Parallel()

	loader, err := config.NewTOMLLoader(filepath.Join("..", "config.example.toml"))
	if err != nil {
		t.Fatalf("NewTOMLLoader() error = %v", err)
	}

	if _, err := loader.Load(); err != nil {
		t.Fatalf("Load(config.example.toml) error = %v", err)
	}
}

func TestNewTOMLLoaderRequiresAPath(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"", "   "} {
		if _, err := config.NewTOMLLoader(path); !errors.Is(err, config.ErrMissingSetting) {
			t.Errorf("NewTOMLLoader(%q) error = %v, want ErrMissingSetting", path, err)
		}
	}
}
