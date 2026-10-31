package catalog_test

import (
	"errors"
	"testing"
	"testing/fstest"

	"github.com/rusher2004/screeny-bird/catalog"
)

func TestLoadRejectsPrivateContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(f *speciesFiles)
		want error
	}{
		{
			name: "private key in the profile",
			edit: func(f *speciesFiles) { f.manifest["profile"].(map[string]any)["latitude"] = 40.1 },
			want: catalog.ErrPrivateField,
		},
		{
			name: "private key with different punctuation and case",
			edit: func(f *speciesFiles) { f.manifest["profile"].(map[string]any)["Postal-Code"] = "12345" },
			want: catalog.ErrPrivateField,
		},
		{
			name: "private key nested inside a list",
			edit: func(f *speciesFiles) {
				f.manifest["references"] = []any{map[string]any{"detail": map[string]any{"refresh_token": "x"}}}
			},
			want: catalog.ErrPrivateField,
		},
		{
			name: "observation count in a manifest",
			edit: func(f *speciesFiles) { f.manifest["observation_count"] = 3 },
			want: catalog.ErrPrivateField,
		},
		{
			name: "absolute POSIX path",
			edit: func(f *speciesFiles) { f.manifest["profile"].(map[string]any)["note"] = "/Users/someone/photo.png" },
			want: catalog.ErrLocalPath,
		},
		{
			name: "file URL",
			edit: func(f *speciesFiles) { f.manifest["profile"].(map[string]any)["note"] = "file:///tmp/photo.png" },
			want: catalog.ErrLocalPath,
		},
		{
			name: "Windows drive path",
			edit: func(f *speciesFiles) { f.manifest["profile"].(map[string]any)["note"] = `C:\photos\bird.png` },
			want: catalog.ErrLocalPath,
		},
		{
			name: "Windows UNC path",
			edit: func(f *speciesFiles) { f.manifest["profile"].(map[string]any)["note"] = `\\server\share\bird.png` },
			want: catalog.ErrLocalPath,
		},
		{
			name: "private key only in a companion file",
			edit: func(f *speciesFiles) {
				f.profile = map[string]any{"taxon_id": 1001, "habitat": "Dense brush", "email": "a@b.test"}
				f.manifest["profile"] = f.profile
			},
			want: catalog.ErrPrivateField,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) { tt.edit(f) })

			if _, err := loadFS(t, fsys); !errors.Is(err, tt.want) {
				t.Fatalf("Load() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsPrivateContentInIndex(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{wren}, nil)
	fsys["index.json"] = &fstest.MapFile{Data: mustJSON(t, map[string]any{
		"schema_version": 1, "generated_at": nil, "species": []any{}, "place_name": "Somewhere",
	})}

	if _, err := loadFS(t, fsys); !errors.Is(err, catalog.ErrPrivateField) {
		t.Fatalf("Load() error = %v, want ErrPrivateField", err)
	}
}

func TestLoadAcceptsOrdinaryProseAndURLs(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) {
		profile := f.manifest["profile"].(map[string]any)
		profile["habitat"] = "Dense brush, 12–13 cm / 4.7–5.1 in; see https://example.test/a/b"
		profile["behavior"] = "Sings from exposed perches (a/b)."
	})

	mustLoad(t, fsys)
}
