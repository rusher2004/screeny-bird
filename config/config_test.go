package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rusher2004/screeny-bird/config"
)

func TestValidateRequiresEverySetting(t *testing.T) {
	t.Parallel()

	tests := map[string]config.Config{
		"zero value":       {},
		"whitespace value": {CatalogDir: "   "},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := cfg.Validate()
			if !errors.Is(err, config.ErrMissingSetting) {
				t.Fatalf("Validate() error = %v, want ErrMissingSetting", err)
			}

			if !strings.Contains(err.Error(), "catalog_dir") {
				t.Errorf("error %q does not name the setting", err)
			}
		})
	}

	if err := (config.Config{CatalogDir: "/data/catalog"}).Validate(); err != nil {
		t.Fatalf("Validate() on a complete config error = %v", err)
	}
}
