package catalog_test

import (
	"errors"
	"testing"
	"testing/fstest"

	"github.com/rusher2004/screeny-bird/catalog"
)

func TestLoadRejectsBadManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(f *speciesFiles)
		want error
	}{
		{
			name: "unsupported schema version",
			edit: func(f *speciesFiles) { f.manifest["schema_version"] = 2 },
			want: catalog.ErrUnsupportedSchema,
		},
		{
			name: "missing schema version",
			edit: func(f *speciesFiles) { delete(f.manifest, "schema_version") },
			want: catalog.ErrUnsupportedSchema,
		},
		{
			name: "pending status",
			edit: func(f *speciesFiles) { f.manifest["status"] = "pending" },
			want: catalog.ErrIdentityMismatch,
		},
		{
			name: "slug differs from directory",
			edit: func(f *speciesFiles) { f.manifest["slug"] = "other-wren" },
			want: catalog.ErrIdentityMismatch,
		},
		{
			name: "slug is a path",
			edit: func(f *speciesFiles) { f.manifest["slug"] = "../escape" },
			want: catalog.ErrIdentityMismatch,
		},
		{
			name: "taxon ID differs from directory",
			edit: func(f *speciesFiles) { f.manifest["taxon_id"] = 9999 },
			want: catalog.ErrIdentityMismatch,
		},
		{
			name: "taxon ID is a string",
			edit: func(f *speciesFiles) { f.manifest["taxon_id"] = "1001" },
			want: catalog.ErrInvalidManifest,
		},
		{
			name: "approved_at is not a timestamp",
			edit: func(f *speciesFiles) { f.manifest["approved_at"] = "yesterday" },
			want: catalog.ErrInvalidManifest,
		},
		{
			name: "common name is empty",
			edit: func(f *speciesFiles) { f.manifest["common_name"] = "" },
			want: catalog.ErrInvalidManifest,
		},
		{
			name: "portrait asset names another file",
			edit: func(f *speciesFiles) {
				f.manifest["assets"].(map[string]any)["portrait"].(map[string]any)["filename"] = "display.png"
			},
			want: catalog.ErrInvalidManifest,
		},
		{
			name: "missing display asset",
			edit: func(f *speciesFiles) { delete(f.manifest["assets"].(map[string]any), "display") },
			want: catalog.ErrInvalidManifest,
		},
		{
			name: "portrait checksum does not match the file",
			edit: func(f *speciesFiles) {
				f.manifest["assets"].(map[string]any)["portrait"].(map[string]any)["sha256"] = sum([]byte("other"))
			},
			want: catalog.ErrChecksumMismatch,
		},
		{
			name: "checksum is uppercase",
			edit: func(f *speciesFiles) {
				f.manifest["assets"].(map[string]any)["display"].(map[string]any)["sha256"] = "ABCDEF"
			},
			want: catalog.ErrChecksumMismatch,
		},
		{
			name: "profile.json differs from the manifest",
			edit: func(f *speciesFiles) { f.profile = map[string]any{"habitat": "somewhere else"} },
			want: catalog.ErrProfileMismatch,
		},
		{
			name: "quality-review.json differs from the manifest",
			edit: func(f *speciesFiles) {
				review := validReview()
				review["species_accuracy"] = 4
				f.review = review
			},
			want: catalog.ErrProfileMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) { tt.edit(f) })

			if _, err := catalog.Load(fsys); !errors.Is(err, tt.want) {
				t.Fatalf("Load() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestLoadRejectsMalformedManifestJSON(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{wren}, nil)
	fsys["species/1001-carolina-wren/manifest.json"] = &fstest.MapFile{Data: []byte("{not json")}

	if _, err := catalog.Load(fsys); !errors.Is(err, catalog.ErrInvalidManifest) {
		t.Fatalf("Load() error = %v, want ErrInvalidManifest", err)
	}
}

func TestLoadAcceptsOptionalCompanionFilesAndExtraManifestFields(t *testing.T) {
	t.Parallel()

	fsys := build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) {
		f.omitProfile = true
		f.omitReview = true
		f.manifest["catalog_migration"] = map[string]any{"reason": "regenerated", "replaces": map[string]any{}}
	})

	mustLoad(t, fsys)
}
