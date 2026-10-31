package catalog_test

import (
	"encoding/json"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/rusher2004/screeny-bird/catalog"
)

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

			if _, err := loadFS(t, fsys); !errors.Is(err, catalog.ErrIndexMismatch) {
				t.Fatalf("Load() error = %v, want ErrIndexMismatch", err)
			}
		})
	}
}

func TestLoadRejectsMalformedIndex(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{wren}, nil)
	fsys["index.json"] = &fstest.MapFile{Data: []byte("[")}

	if _, err := loadFS(t, fsys); !errors.Is(err, catalog.ErrIndexMismatch) {
		t.Fatalf("Load() error = %v, want ErrIndexMismatch", err)
	}
}
