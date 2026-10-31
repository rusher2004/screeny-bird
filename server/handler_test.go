package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileSum(t *testing.T, path string) (data []byte, digest string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	sum := sha256.Sum256(data)

	return data, hex.EncodeToString(sum[:])
}

func TestHealthReportsTheServedCatalog(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren, finch).start()

	resp := h.get("/healthz")
	if resp.status != http.StatusOK || resp.header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /healthz = %d %q, want 200 JSON", resp.status, resp.header.Get("Content-Type"))
	}

	var body map[string]any
	if err := json.Unmarshal(resp.body, &body); err != nil {
		t.Fatalf("decoding health: %v", err)
	}

	if body["ok"] != true || body["version"] != "9.9.9" || body["species"] != float64(2) || body["generated_at"] != "2026-08-09T17:12:25+00:00" {
		t.Fatalf("health = %v, want ok, the version, 2 species and the latest approval", body)
	}

	if got := resp.header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestEveryResponseIsNosniff(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	for _, path := range []string{"/healthz", "/v1/catalog", "/v1/assets/species/" + wren + "/portrait.png", "/v1/assets/nope.png", "/nope"} {
		if got := h.get(path).header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s: X-Content-Type-Options = %q, want nosniff", path, got)
		}
	}

	if got := h.do(http.MethodPost, "/healthz", nil).header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("POST /healthz: X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestCatalogServesTheValidatedSpecies(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren, finch, bunting).start()

	resp := h.get("/v1/catalog")
	if resp.status != http.StatusOK || resp.header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /v1/catalog = %d %q, want 200 JSON", resp.status, resp.header.Get("Content-Type"))
	}

	var body struct {
		GeneratedAt   string `json:"generated_at"`
		SchemaVersion int    `json:"schema_version"`
		Species       []struct {
			ApprovedAt     string `json:"approved_at"`
			CommonName     string `json:"common_name"`
			DisplayPath    string `json:"display_path"`
			DisplaySHA256  string `json:"display_sha256"`
			PortraitPath   string `json:"portrait_path"`
			PortraitSHA256 string `json:"portrait_sha256"`
			ScientificName string `json:"scientific_name"`
			Slug           string `json:"slug"`
			TaxonID        int    `json:"taxon_id"`
		} `json:"species"`
	}

	if err := json.Unmarshal(resp.body, &body); err != nil {
		t.Fatalf("decoding catalog: %v", err)
	}

	if body.SchemaVersion != 1 || body.GeneratedAt != "2026-08-09T17:12:25+00:00" || len(body.Species) != 3 {
		t.Fatalf("catalog = %+v, want schema 1, the latest approval and 3 species", body)
	}

	// Sorted by case-folded common name: Carolina Wren, House Finch, Painted Bunting.
	first := body.Species[0]
	_, portraitSum := fileSum(t, filepath.Join(h.dir, "species", wren, "portrait.png"))

	if first.TaxonID != 1001 || first.Slug != "carolina-wren" || first.PortraitSHA256 != portraitSum ||
		first.PortraitPath != "species/"+wren+"/portrait.png" || first.DisplayPath != "species/"+wren+"/display.png" {
		t.Fatalf("first species = %+v, does not match the fixture", first)
	}

	// Exactly the documented projection: no private or extra fields.
	var raw struct {
		Species []map[string]any `json:"species"`
	}

	if err := json.Unmarshal(resp.body, &raw); err != nil {
		t.Fatalf("decoding raw catalog: %v", err)
	}

	if len(raw.Species[0]) != 9 {
		t.Errorf("species has %d fields, want exactly the 9 documented ones: %v", len(raw.Species[0]), raw.Species[0])
	}
}

func TestEmptyCatalogServesAnEmptyList(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}).start()

	resp := h.get("/v1/catalog")
	if resp.status != http.StatusOK || !strings.Contains(string(resp.body), `"species":[]`) || !strings.Contains(string(resp.body), `"generated_at":null`) {
		t.Fatalf("GET /v1/catalog = %d %s, want an empty species list and a null generated_at", resp.status, resp.body)
	}
}

func TestCatalogSupportsConditionalRequests(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	first := h.get("/v1/catalog")

	etag := first.header.Get("ETag")
	if etag == "" || first.header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("first response ETag %q, Cache-Control %q; want an ETag and no-cache", etag, first.header.Get("Cache-Control"))
	}

	for name, header := range map[string]string{"exact": etag, "weak": "W/" + etag, "list": `"other", ` + etag, "star": "*"} {
		resp := h.do(http.MethodGet, "/v1/catalog", map[string]string{"If-None-Match": header})
		if resp.status != http.StatusNotModified || len(resp.body) != 0 {
			t.Errorf("%s If-None-Match: status %d with %d body bytes, want an empty 304", name, resp.status, len(resp.body))
		}
	}

	if resp := h.do(http.MethodGet, "/v1/catalog", map[string]string{"If-None-Match": `"stale"`}); resp.status != http.StatusOK {
		t.Errorf("a different ETag got %d, want 200", resp.status)
	}
}

func TestHeadReturnsHeadersWithoutABody(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	for _, path := range []string{"/healthz", "/v1/catalog", "/v1/assets/species/" + wren + "/display.png"} {
		resp := h.do(http.MethodHead, path, nil)
		if resp.status != http.StatusOK || len(resp.body) != 0 {
			t.Errorf("HEAD %s = %d with %d body bytes, want 200 and no body", path, resp.status, len(resp.body))
		}
	}
}

func TestAssetServesVerifiedBytes(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()
	want, digest := fileSum(t, filepath.Join(h.dir, "species", wren, "portrait.png"))
	path := "/v1/assets/species/" + wren + "/portrait.png"

	resp := h.get(path)
	if resp.status != http.StatusOK || string(resp.body) != string(want) {
		t.Fatalf("GET %s = %d with %d bytes, want 200 with the %d byte file", path, resp.status, len(resp.body), len(want))
	}

	if resp.header.Get("Content-Type") != "image/png" || resp.header.Get("ETag") != `"`+digest+`"` {
		t.Errorf("Content-Type %q, ETag %q; want image/png and the digest", resp.header.Get("Content-Type"), resp.header.Get("ETag"))
	}

	if resp.header.Get("Cache-Control") != "no-cache" {
		t.Errorf("an unaddressed request got Cache-Control %q, want no-cache", resp.header.Get("Cache-Control"))
	}
}

func TestAssetIsImmutableOnlyWhenAddressedByItsDigest(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()
	_, digest := fileSum(t, filepath.Join(h.dir, "species", wren, "display.png"))
	path := "/v1/assets/species/" + wren + "/display.png"

	tests := map[string]struct{ query, want string }{
		"matching digest": {"?sha256=" + digest, "public, max-age=31536000, immutable"},
		"wrong digest":    {"?sha256=" + strings.Repeat("0", 64), "no-cache"},
		"two digests":     {"?sha256=" + digest + "&sha256=" + digest, "no-cache"},
		"no digest":       {"", "no-cache"},
	}

	for name, tt := range tests {
		resp := h.get(path + tt.query)
		if resp.status != http.StatusOK || resp.header.Get("Cache-Control") != tt.want {
			t.Errorf("%s: status %d, Cache-Control %q; want 200 and %q", name, resp.status, resp.header.Get("Cache-Control"), tt.want)
		}
	}
}

func TestAssetConditionalRequestsSkipTheFile(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()
	_, digest := fileSum(t, filepath.Join(h.dir, "species", wren, "display.png"))

	before := h.store.reads.Load()
	resp := h.do(http.MethodGet, "/v1/assets/species/"+wren+"/display.png", map[string]string{"If-None-Match": `"` + digest + `"`})

	if resp.status != http.StatusNotModified || len(resp.body) != 0 {
		t.Fatalf("conditional GET = %d with %d bytes, want an empty 304", resp.status, len(resp.body))
	}

	if after := h.store.reads.Load(); after != before {
		t.Errorf("a 304 read the file %d times, want 0", after-before)
	}
}

func TestOnlyCatalogImagesAreServed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	for _, path := range []string{
		"/v1/assets/index.json",
		"/v1/assets/species/" + wren + "/manifest.json",
		"/v1/assets/species/" + wren + "/profile.json",
		"/v1/assets/species/" + finch + "/portrait.png",
		"/v1/assets/species/" + wren + "/other.png",
		"/v1/assets/species/%2e%2e/index.json",
		"/v1/assets/..%2findex.json",
		"/v1/assets/species/" + wren + "/portrait.png/",
		"/v1/assets/",
		"/v1/assets",
	} {
		resp := h.get(path)
		switch resp.status {
		case http.StatusNotFound, http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		default:
			t.Errorf("GET %s = %d, want 404 or a redirect", path, resp.status)
		}

		if resp.status == http.StatusOK || strings.Contains(string(resp.body), "schema_version") {
			t.Errorf("GET %s served content: %q", path, resp.body)
		}
	}
}

func TestATamperedAssetIsNeverSent(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()
	file := filepath.Join(h.dir, "species", wren, "portrait.png")
	original, _ := fileSum(t, file)

	if err := os.WriteFile(file, append(original, 'x'), 0o644); err != nil {
		t.Fatalf("tampering: %v", err)
	}

	resp := h.get("/v1/assets/species/" + wren + "/portrait.png")
	if resp.status != http.StatusServiceUnavailable {
		t.Fatalf("GET of a tampered asset = %d, want 503", resp.status)
	}

	if len(resp.body) >= len(original) || strings.HasPrefix(string(resp.body), "\x89PNG") {
		t.Fatalf("a tampered file's bytes reached the client (%d bytes)", len(resp.body))
	}

	if resp.header.Get("ETag") != "" || resp.header.Get("Cache-Control") != "no-store" {
		t.Errorf("an unavailable asset kept ETag %q, Cache-Control %q; want none and no-store", resp.header.Get("ETag"), resp.header.Get("Cache-Control"))
	}
}

func TestAnUnreadableAssetIsUnavailableNotMissing(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	if err := os.Remove(filepath.Join(h.dir, "species", wren, "display.png")); err != nil {
		t.Fatalf("removing file: %v", err)
	}

	if resp := h.get("/v1/assets/species/" + wren + "/display.png"); resp.status != http.StatusServiceUnavailable {
		t.Fatalf("GET of a deleted asset = %d, want 503", resp.status)
	}
}

func TestASymlinkedAssetIsNeverFollowed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()
	portrait := filepath.Join(h.dir, "species", wren, "portrait.png")

	if err := os.Remove(portrait); err != nil {
		t.Fatalf("removing portrait: %v", err)
	}

	// Even a link to the correct bytes inside the catalog is refused.
	if err := os.Symlink(filepath.Join(h.dir, "species", wren, "display.png"), portrait); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	if resp := h.get("/v1/assets/species/" + wren + "/portrait.png"); resp.status != http.StatusServiceUnavailable {
		t.Fatalf("GET of a symlinked asset = %d, want 503", resp.status)
	}
}

func TestOnlyGetAndHeadAreAllowed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"https://app.example.test"}, wren).start()

	for _, path := range []string{"/healthz", "/v1/catalog", "/v1/assets/species/" + wren + "/portrait.png"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions} {
			resp := h.do(method, path, map[string]string{"Origin": "https://app.example.test"})
			if resp.status != http.StatusMethodNotAllowed || resp.header.Get("Allow") != "GET, HEAD" {
				t.Errorf("%s %s = %d Allow %q, want 405 and GET, HEAD", method, path, resp.status, resp.header.Get("Allow"))
			}

			if resp.header.Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s %s granted CORS access on a rejected method", method, path)
			}
		}
	}
}

func TestUnknownPathsAreJSONNotFound(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	resp := h.get("/nope")
	if resp.status != http.StatusNotFound || resp.header.Get("Content-Type") != "application/json" || !strings.Contains(string(resp.body), `"ok":false`) {
		t.Fatalf("GET /nope = %d %q %s, want a JSON 404", resp.status, resp.header.Get("Content-Type"), resp.body)
	}
}

func TestCORSIsOffUnlessAnOriginIsListed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{"https://app.example.test"}, wren).start()
	asset := "/v1/assets/species/" + wren + "/portrait.png"

	for _, path := range []string{"/v1/catalog", asset} {
		allowed := h.do(http.MethodGet, path, map[string]string{"Origin": "https://app.example.test"})
		if got := allowed.header.Get("Access-Control-Allow-Origin"); got != "https://app.example.test" {
			t.Errorf("GET %s from a listed origin: Allow-Origin = %q, want that exact origin", path, got)
		}

		for name, origin := range map[string]string{
			"unlisted":     "https://evil.example.test",
			"scheme":       "http://app.example.test",
			"port":         "https://app.example.test:8443",
			"trailing":     "https://app.example.test/",
			"case":         "https://APP.example.test",
			"null":         "null",
			"star":         "*",
			"prefix match": "https://app.example.test.evil.test",
		} {
			resp := h.do(http.MethodGet, path, map[string]string{"Origin": origin})
			if got := resp.header.Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("GET %s from %s origin %q: Allow-Origin = %q, want none", path, name, origin, got)
			}
		}

		for _, resp := range []response{allowed, h.get(path)} {
			if vary := resp.header.Values("Vary"); !strings.Contains(strings.Join(vary, ","), "Origin") {
				t.Errorf("GET %s: Vary = %v, want it to include Origin", path, vary)
			}

			if resp.header.Get("Access-Control-Allow-Credentials") != "" || resp.header.Get("Access-Control-Allow-Origin") == "*" {
				t.Errorf("GET %s sent credentials or a wildcard CORS grant: %v", path, resp.header)
			}
		}
	}

	if resp := h.do(http.MethodGet, "/healthz", map[string]string{"Origin": "https://app.example.test"}); resp.header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("/healthz granted CORS access")
	}
}

func TestNoOriginIsAllowedWhenTheListIsEmpty(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	resp := h.do(http.MethodGet, "/v1/catalog", map[string]string{"Origin": "https://app.example.test"})
	if got := resp.header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q with an empty origin list, want none", got)
	}
}

func TestEveryRequestIsLoggedWithoutItsQuery(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	h.get("/healthz")
	h.get("/v1/assets/species/" + wren + "/display.png?sha256=" + strings.Repeat("a", 64))
	h.do(http.MethodPost, "/v1/catalog", nil)
	h.get("/nope%01x")
	h.do(http.MethodHead, "/v1/catalog", nil)

	entries := h.log.all()
	if len(entries) != 5 {
		t.Fatalf("logged %d requests, want 5: %v", len(entries), entries)
	}

	if e := entries[4]; e.method != "HEAD" || e.status != 200 || e.bytes != 0 {
		t.Errorf("HEAD entry = %+v, want 200 with 0 bytes, because a HEAD response has no body", e)
	}

	if e := entries[0]; e.method != "GET" || e.path != "/healthz" || e.status != 200 || e.bytes == 0 {
		t.Errorf("first entry = %+v, want GET /healthz 200 with a body", e)
	}

	if e := entries[1]; e.path != "/v1/assets/species/"+wren+"/display.png" || e.status != 200 {
		t.Errorf("asset entry = %+v, want the path without its query", e)
	}

	if e := entries[2]; e.method != "POST" || e.status != 405 {
		t.Errorf("rejected entry = %+v, want POST 405", e)
	}

	for _, e := range entries {
		if strings.ContainsAny(e.path, "\x00\x01\x02\n\r") {
			t.Errorf("logged a path with control characters: %q", e.path)
		}
	}
}
