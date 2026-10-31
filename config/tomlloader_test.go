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

// complete is a config file with every setting; edit returns it with changes.
const complete = `catalog_dir = "/data/catalog"
work_dir = "/data/work"
upstream_repo = "veteranbv/inky-bird-frame"
upstream_ref = "main"
upstream_timeout_seconds = 30
`

func TestLoadReadsEverySetting(t *testing.T) {
	t.Parallel()

	got, err := load(t, "# comments are fine\n"+complete)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := config.Config{
		CatalogDir:             "/data/catalog",
		WorkDir:                "/data/work",
		UpstreamRepo:           "veteranbv/inky-bird-frame",
		UpstreamRef:            "main",
		UpstreamTimeoutSeconds: 30,
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadRequiresEverySetting(t *testing.T) {
	t.Parallel()

	settings := []string{"catalog_dir", "work_dir", "upstream_repo", "upstream_ref", "upstream_timeout_seconds"}

	for _, missing := range settings {
		t.Run("without "+missing, func(t *testing.T) {
			t.Parallel()

			var lines []string

			for line := range strings.SplitSeq(strings.TrimSpace(complete), "\n") {
				if !strings.HasPrefix(line, missing+" ") {
					lines = append(lines, line)
				}
			}

			_, err := load(t, strings.Join(lines, "\n")+"\n")
			if !errors.Is(err, config.ErrMissingSetting) || !strings.Contains(err.Error(), missing) {
				t.Fatalf("Load() error = %v, want ErrMissingSetting naming %s", err, missing)
			}
		})
	}

	for name, content := range map[string]string{
		"empty string":      strings.Replace(complete, `catalog_dir = "/data/catalog"`, `catalog_dir = ""`, 1),
		"whitespace string": strings.Replace(complete, `work_dir = "/data/work"`, `work_dir = "   "`, 1),
		"zero timeout":      strings.Replace(complete, "= 30", "= 0", 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := load(t, content); !errors.Is(err, config.ErrMissingSetting) {
				t.Fatalf("Load() error = %v, want ErrMissingSetting", err)
			}
		})
	}
}

func TestLoadReportsEveryMissingSettingTogether(t *testing.T) {
	t.Parallel()

	_, err := load(t, "")
	if !errors.Is(err, config.ErrMissingSetting) {
		t.Fatalf("Load() error = %v, want ErrMissingSetting", err)
	}

	for _, name := range []string{"catalog_dir", "work_dir", "upstream_repo", "upstream_ref", "upstream_timeout_seconds"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

func TestLoadRejectsANegativeTimeout(t *testing.T) {
	t.Parallel()

	_, err := load(t, strings.Replace(complete, "= 30", "= -5", 1))
	if !errors.Is(err, config.ErrInvalidConfig) || !strings.Contains(err.Error(), "upstream_timeout_seconds") {
		t.Fatalf("Load() error = %v, want ErrInvalidConfig naming upstream_timeout_seconds", err)
	}
}

func TestLoadRejectsInvalidFiles(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"unknown key":      complete + "listen = \":8080\"\n",
		"malformed TOML":   "catalog_dir = \n",
		"wrong value type": strings.Replace(complete, `catalog_dir = "/data/catalog"`, "catalog_dir = 5", 1),
		"duplicate key":    complete + "catalog_dir = \"/b\"\n",
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

	_, err := load(t, complete+"listen = \":8080\"\n")
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
