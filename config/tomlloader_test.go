package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
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
derived_dir = "/data/derived"
derive_width = 3840
derive_height = 2160
upstream_repo = "veteranbv/inky-bird-frame"
upstream_ref = "main"
upstream_timeout_seconds = 30
upstream_attempts = 3
upstream_retry_delay_seconds = 5
listen_address = ":8080"
read_header_timeout_seconds = 10
write_timeout_seconds = 60
shutdown_timeout_seconds = 15
reload_interval_seconds = 60
healthcheck_timeout_seconds = 5
cors_allowed_origins = ["https://app.example.test"]
`

var allSettings = []string{
	"catalog_dir", "work_dir", "derived_dir", "derive_width", "derive_height", "upstream_repo", "upstream_ref", "upstream_timeout_seconds",
	"upstream_attempts", "upstream_retry_delay_seconds",
	"listen_address", "read_header_timeout_seconds", "write_timeout_seconds", "shutdown_timeout_seconds",
	"reload_interval_seconds", "healthcheck_timeout_seconds", "cors_allowed_origins",
}

func TestLoadReadsEverySetting(t *testing.T) {
	t.Parallel()

	got, err := load(t, "# comments are fine\n"+complete)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := config.Config{
		CatalogDir:             "/data/catalog",
		WorkDir:                "/data/work",
		DerivedDir:             "/data/derived",
		DeriveWidth:            3840,
		DeriveHeight:           2160,
		UpstreamRepo:           "veteranbv/inky-bird-frame",
		UpstreamRef:            "main",
		UpstreamTimeoutSeconds: 30,

		UpstreamAttempts:          3,
		UpstreamRetryDelaySeconds: 5,
		ListenAddress:             ":8080",
		ReadHeaderTimeoutSeconds:  10,
		WriteTimeoutSeconds:       60,
		ShutdownTimeoutSeconds:    15,
		ReloadIntervalSeconds:     60,
		HealthcheckTimeoutSeconds: 5,
		CORSAllowedOrigins:        []string{"https://app.example.test"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadRequiresEverySetting(t *testing.T) {
	t.Parallel()

	settings := allSettings

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
		"zero attempts":     strings.Replace(complete, "upstream_attempts = 3", "upstream_attempts = 0", 1),
		"zero retry delay":  strings.Replace(complete, "upstream_retry_delay_seconds = 5", "upstream_retry_delay_seconds = 0", 1),
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

	for _, name := range allSettings {
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

func TestLoadAcceptsAnExplicitlyEmptyOriginList(t *testing.T) {
	t.Parallel()

	got, err := load(t, strings.Replace(complete, `cors_allowed_origins = ["https://app.example.test"]`, "cors_allowed_origins = []", 1))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.CORSAllowedOrigins == nil || len(got.CORSAllowedOrigins) != 0 {
		t.Fatalf("CORSAllowedOrigins = %#v, want an empty, non-nil list", got.CORSAllowedOrigins)
	}
}

func TestLoadRejectsUnusableServerSettings(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ from, to, want string }{
		"negative derive width":      {"derive_width = 3840", "derive_width = -1", "derive_width"},
		"negative derive height":     {"derive_height = 2160", "derive_height = -1", "derive_height"},
		"derived dir is the catalog": {`derived_dir = "/data/derived"`, `derived_dir = "/data/catalog"`, "derived_dir"},
		"derived dir inside catalog": {`derived_dir = "/data/derived"`, `derived_dir = "/data/catalog/derived"`, "derived_dir"},
		"negative attempts":          {"upstream_attempts = 3", "upstream_attempts = -1", "upstream_attempts"},
		"negative retry delay":       {"upstream_retry_delay_seconds = 5", "upstream_retry_delay_seconds = -1", "upstream_retry_delay_seconds"},
		"negative write timeout":     {"write_timeout_seconds = 60", "write_timeout_seconds = -1", "write_timeout_seconds"},
		"negative reload":            {"reload_interval_seconds = 60", "reload_interval_seconds = -1", "reload_interval_seconds"},
		"listen without a port":      {`listen_address = ":8080"`, `listen_address = "localhost"`, "listen_address"},
		"listen port not numeric":    {`listen_address = ":http"`, `listen_address = ":http"`, "listen_address"},
		"listen port too large":      {`listen_address = ":8080"`, `listen_address = ":70000"`, "listen_address"},
		"wildcard origin":            {`["https://app.example.test"]`, `["*"]`, "cors_allowed_origins"},
		"origin with a path":         {`["https://app.example.test"]`, `["https://app.example.test/x"]`, "cors_allowed_origins"},
		"origin without scheme":      {`["https://app.example.test"]`, `["app.example.test"]`, "cors_allowed_origins"},
		"origin with a wildcard":     {`["https://app.example.test"]`, `["https://*.example.test"]`, "cors_allowed_origins"},
		"origin with credentials":    {`["https://app.example.test"]`, `["https://user@app.example.test"]`, "cors_allowed_origins"},
		"ftp origin":                 {`["https://app.example.test"]`, `["ftp://app.example.test"]`, "cors_allowed_origins"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			content := strings.Replace(complete, tt.from, tt.to, 1)
			if name == "listen port not numeric" {
				content = strings.Replace(complete, `listen_address = ":8080"`, `listen_address = ":http"`, 1)
			}

			_, err := load(t, content)
			if !errors.Is(err, config.ErrInvalidConfig) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want ErrInvalidConfig naming %s", err, tt.want)
			}
		})
	}
}
