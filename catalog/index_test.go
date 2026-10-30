package catalog_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rusher2004/screeny-bird/catalog"
)

func TestIndexProjectsSortedSpecies(t *testing.T) {
	t.Parallel()

	index := mustLoad(t, build(t, []speciesFixture{bunting, wren, finch}, nil)).Index()

	if index.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", index.SchemaVersion)
	}

	if index.GeneratedAt == nil || *index.GeneratedAt != finch.ApprovedAt {
		t.Errorf("GeneratedAt = %v, want the latest approval %q", index.GeneratedAt, finch.ApprovedAt)
	}

	var order []string
	for _, s := range index.Species {
		order = append(order, s.Slug)
	}

	if got, want := strings.Join(order, ","), "carolina-wren,house-finch,painted-bunting"; got != want {
		t.Errorf("species order = %s, want %s", got, want)
	}
}

func TestIndexOfEmptyCatalogHasNullGeneratedAt(t *testing.T) {
	t.Parallel()

	fsys := build(t, nil, nil)
	fsys["species"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}

	data, err := mustLoad(t, fsys).Index().JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}

	want := "{\n  \"generated_at\": null,\n  \"schema_version\": 1,\n  \"species\": []\n}\n"
	if string(data) != want {
		t.Fatalf("JSON() = %q, want %q", data, want)
	}
}

func TestIndexJSONIsCanonical(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{finch, wren}, nil)
	data, err := mustLoad(t, fsys).Index().JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}

	if !bytes.HasSuffix(data, []byte("}\n")) {
		t.Errorf("JSON() does not end with a newline: %q", data[len(data)-5:])
	}

	// Keys are sorted at every level and indented by two spaces.
	for _, fragment := range []string{
		"{\n  \"generated_at\": \"2026-08-09T17:12:25+00:00\",\n  \"schema_version\": 1,\n  \"species\": [\n    {\n      \"approved_at\"",
		"\"common_name\": \"Carolina Wren\",\n      \"display_path\"",
		"\"scientific_name\"",
		"\"taxon_id\": 1001\n    }",
	} {
		if !bytes.Contains(data, []byte(fragment)) {
			t.Errorf("JSON() lacks %q:\n%s", fragment, data)
		}
	}

	var decoded catalog.Index
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("JSON() output does not decode: %v", err)
	}

	again, err := decoded.JSON()
	if err != nil {
		t.Fatalf("JSON() of the decoded index error = %v", err)
	}

	if !bytes.Equal(data, again) {
		t.Errorf("JSON() is not stable across a decode round trip:\n%s\nvs\n%s", data, again)
	}
}

func TestLoadRejectsIndexThatDoesNotMatchManifests(t *testing.T) {
	t.Parallel()

	base := func(t *testing.T) (fstest.MapFS, map[string]any) {
		t.Helper()

		fsys := build(t, []speciesFixture{wren, finch}, nil)

		var index map[string]any
		if err := json.Unmarshal(fsys["index.json"].Data, &index); err != nil {
			t.Fatalf("decoding fixture index: %v", err)
		}

		return fsys, index
	}

	tests := []struct {
		name string
		edit func(index map[string]any)
	}{
		{name: "entry missing", edit: func(i map[string]any) { i["species"] = i["species"].([]any)[:1] }},
		{
			name: "entry changed",
			edit: func(i map[string]any) { i["species"].([]any)[0].(map[string]any)["display_sha256"] = sum([]byte("x")) },
		},
		{
			name: "entries out of order",
			edit: func(i map[string]any) {
				s := i["species"].([]any)
				s[0], s[1] = s[1], s[0]
			},
		},
		{name: "wrong generated_at", edit: func(i map[string]any) { i["generated_at"] = "2020-01-01T00:00:00+00:00" }},
		{name: "unsupported schema", edit: func(i map[string]any) { i["schema_version"] = 2 }},
		{name: "unknown top-level field", edit: func(i map[string]any) { i["extra"] = true }},
		{
			name: "unknown entry field",
			edit: func(i map[string]any) { i["species"].([]any)[0].(map[string]any)["latest_detection_at"] = "x" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys, index := base(t)
			tt.edit(index)
			fsys["index.json"] = &fstest.MapFile{Data: mustJSON(t, index)}

			if _, err := catalog.Load(fsys); !errors.Is(err, catalog.ErrIndexMismatch) {
				t.Fatalf("Load() error = %v, want ErrIndexMismatch", err)
			}
		})
	}
}

func TestLoadRejectsMalformedIndex(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{wren}, nil)
	fsys["index.json"] = &fstest.MapFile{Data: []byte("[")}

	if _, err := catalog.Load(fsys); !errors.Is(err, catalog.ErrIndexMismatch) {
		t.Fatalf("Load() error = %v, want ErrIndexMismatch", err)
	}
}
