// Package config defines the service's settings and the loaders that supply
// them. Every setting is required and there are no defaults.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
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
	// UpstreamAttempts is how many times a request to the upstream repository
	// is tried in all, counting the first, before sync gives up on it.
	UpstreamAttempts int `toml:"upstream_attempts"`
	// UpstreamRetryDelaySeconds is the wait between those attempts.
	UpstreamRetryDelaySeconds int `toml:"upstream_retry_delay_seconds"`
	// DerivedDir is where `catalog derive` writes the screensaver-sized images.
	// It must be outside CatalogDir, which holds only approved plates.
	DerivedDir string `toml:"derived_dir"`
	// DeriveWidth and DeriveHeight are the pixel size of each derived image.
	DeriveWidth  int `toml:"derive_width"`
	DeriveHeight int `toml:"derive_height"`
	// ListenAddress is the host:port the HTTP server listens on, such as ":8080".
	ListenAddress string `toml:"listen_address"`
	// ReadHeaderTimeoutSeconds bounds how long a client may take to send request headers.
	ReadHeaderTimeoutSeconds int `toml:"read_header_timeout_seconds"`
	// WriteTimeoutSeconds bounds how long writing one response may take.
	WriteTimeoutSeconds int `toml:"write_timeout_seconds"`
	// ShutdownTimeoutSeconds bounds how long in-flight requests get to finish on shutdown.
	ShutdownTimeoutSeconds int `toml:"shutdown_timeout_seconds"`
	// ReloadIntervalSeconds is how often the server checks whether the catalog changed.
	ReloadIntervalSeconds int `toml:"reload_interval_seconds"`
	// HealthcheckTimeoutSeconds bounds the healthcheck command's request.
	HealthcheckTimeoutSeconds int `toml:"healthcheck_timeout_seconds"`
	// CORSAllowedOrigins lists the browser origins allowed to read the catalog
	// and images, each as scheme://host[:port]. An empty list allows none.
	CORSAllowedOrigins []string `toml:"cors_allowed_origins"`
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
		{"derived_dir", c.DerivedDir},
		{"upstream_repo", c.UpstreamRepo},
		{"upstream_ref", c.UpstreamRef},
		{"listen_address", c.ListenAddress},
	} {
		if strings.TrimSpace(setting.value) == "" {
			errs = append(errs, fmt.Errorf("%s: %w", setting.name, ErrMissingSetting))
		}
	}

	for _, setting := range []struct {
		name  string
		value int
	}{
		{"upstream_timeout_seconds", c.UpstreamTimeoutSeconds},
		{"upstream_attempts", c.UpstreamAttempts},
		{"upstream_retry_delay_seconds", c.UpstreamRetryDelaySeconds},
		{"derive_width", c.DeriveWidth},
		{"derive_height", c.DeriveHeight},
		{"read_header_timeout_seconds", c.ReadHeaderTimeoutSeconds},
		{"write_timeout_seconds", c.WriteTimeoutSeconds},
		{"shutdown_timeout_seconds", c.ShutdownTimeoutSeconds},
		{"reload_interval_seconds", c.ReloadIntervalSeconds},
		{"healthcheck_timeout_seconds", c.HealthcheckTimeoutSeconds},
	} {
		switch {
		case setting.value == 0:
			errs = append(errs, fmt.Errorf("%s: %w", setting.name, ErrMissingSetting))
		case setting.value < 0:
			errs = append(errs, fmt.Errorf("%s: must be positive, got %d: %w", setting.name, setting.value, ErrInvalidConfig))
		}
	}

	if strings.TrimSpace(c.CatalogDir) != "" && strings.TrimSpace(c.DerivedDir) != "" && isWithin(c.CatalogDir, c.DerivedDir) {
		errs = append(errs, fmt.Errorf("derived_dir: %q must be outside catalog_dir %q: %w", c.DerivedDir, c.CatalogDir, ErrInvalidConfig))
	}

	if strings.TrimSpace(c.ListenAddress) != "" {
		if err := checkListenAddress(c.ListenAddress); err != nil {
			errs = append(errs, fmt.Errorf("listen_address: %w: %w", ErrInvalidConfig, err))
		}
	}

	if c.CORSAllowedOrigins == nil {
		errs = append(errs, fmt.Errorf("cors_allowed_origins: %w", ErrMissingSetting))
	}

	for _, origin := range c.CORSAllowedOrigins {
		if err := checkOrigin(origin); err != nil {
			errs = append(errs, fmt.Errorf("cors_allowed_origins: %q: %w: %w", origin, ErrInvalidConfig, err))
		}
	}

	return errors.Join(errs...)
}

// isWithin reports whether path is parent or inside it, comparing cleaned
// paths without touching the filesystem.
func isWithin(parent, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(path))

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func checkListenAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("must look like host:port: %w", err)
	}

	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("port %q is not a number from 0 to 65535", port)
	}

	return nil
}

// checkOrigin accepts only scheme://host[:port]: no wildcard, path, query,
// fragment or credentials.
func checkOrigin(origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("not a URL: %w", err)
	}

	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || strings.Contains(parsed.Host, "*") {
		return errors.New("must be an http or https origin with a host")
	}

	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return errors.New("must be only scheme://host[:port]")
	}

	return nil
}
