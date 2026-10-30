package catalog

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// privateKeys are field names that must never appear in public catalog JSON.
// Matching ignores case and punctuation, so "Postal-Code" matches postal_code.
var privateKeys = normalizeAll(
	"api_key", "apikey", "authorization_confirmed_at", "birdnet_analyzer", "birdnet_go_url",
	"birdbuddy", "catalog_dir", "checkout_dir", "controller_url", "coordinates", "country_code",
	"ebird_archive", "geocoder", "geocoder_attribution", "history_started_at", "geoapify_api_key",
	"geoapify_api_key_env", "latitude", "longitude", "last_imported_at", "email",
	"observation_count", "observed_on", "password", "submission_id", "place_name", "postal_code",
	"postcard_id", "radius_km", "refresh_token", "state_dir", "feeder_id", "workspace_dir", "zip_code",
)

var (
	nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]`)
	windowsDrive    = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	windowsUNC      = regexp.MustCompile(`^(?:\\\\|//)[^\\/]`)
)

func normalizeKey(key string) string {
	return nonAlphanumeric.ReplaceAllString(strings.ToLower(key), "")
}

func normalizeAll(keys ...string) []string {
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = normalizeKey(key)
	}

	return out
}

func isLocalPath(value string) bool {
	return strings.HasPrefix(value, "file://") ||
		strings.HasPrefix(value, "/") ||
		windowsDrive.MatchString(value) ||
		windowsUNC.MatchString(value)
}

// scanPrivacy walks decoded JSON and reports the first private field name or
// local filesystem path. source names the file for the error message.
func scanPrivacy(value any, source string) error {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if slices.Contains(privateKeys, normalizeKey(key)) {
				return fmt.Errorf("%s has field %q: %w", source, key, ErrPrivateField)
			}

			if err := scanPrivacy(child, source); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := scanPrivacy(child, source); err != nil {
				return err
			}
		}
	case string:
		if isLocalPath(v) {
			return fmt.Errorf("%s has a local path value: %w", source, ErrLocalPath)
		}
	}

	return nil
}
