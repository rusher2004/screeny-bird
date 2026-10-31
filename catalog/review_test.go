package catalog_test

import (
	"errors"
	"testing"

	"github.com/rusher2004/screeny-bird/catalog"
)

func TestLoadEnforcesReviewGates(t *testing.T) {
	t.Parallel()

	source := func(title, url string) map[string]any {
		return map[string]any{"title": title, "url": url}
	}

	tests := []struct {
		name   string
		edit   func(f *speciesFiles)
		accept bool
	}{
		{name: "automated review passes", edit: func(*speciesFiles) {}, accept: true},
		{
			name: "review did not pass",
			edit: func(f *speciesFiles) { f.qualityReview()["passed"] = false },
		},
		{
			name: "review is not location free",
			edit: func(f *speciesFiles) { f.qualityReview()["location_free"] = false },
		},
		{
			name: "species accuracy below four",
			edit: func(f *speciesFiles) { f.qualityReview()["species_accuracy"] = 3 },
		},
		{
			name: "anatomy accuracy below four",
			edit: func(f *speciesFiles) { f.qualityReview()["anatomy_accuracy"] = 3 },
		},
		{
			name: "text accuracy below four",
			edit: func(f *speciesFiles) { f.qualityReview()["text_accuracy"] = 3 },
		},
		{
			name: "composition quality below four",
			edit: func(f *speciesFiles) { f.qualityReview()["composition_quality"] = 3 },
		},
		{
			name: "score of exactly four passes",
			edit: func(f *speciesFiles) { f.qualityReview()["composition_quality"] = 4 },

			accept: true,
		},
		{
			name: "outstanding correction findings",
			edit: func(f *speciesFiles) { f.qualityReview()["correction_findings"] = []any{"fix the bill"} },
		},
		{
			name: "unresolved profile conflicts",
			edit: func(f *speciesFiles) {
				f.qualityReview()["profile_conflicts"] = []any{map[string]any{"field": "habitat"}}
			},
		},
		{
			name: "only one verification source",
			edit: func(f *speciesFiles) {
				f.qualityReview()["verification_sources"] = []any{source("One", "https://example.test/one")}
			},
		},
		{
			name: "two sources with the same URL",
			edit: func(f *speciesFiles) {
				f.qualityReview()["verification_sources"] = []any{
					source("One", "https://example.test/same"), source("Two", "https://example.test/same"),
				}
			},
		},
		{
			name: "source is not https",
			edit: func(f *speciesFiles) {
				f.qualityReview()["verification_sources"] = []any{
					source("One", "https://example.test/one"), source("Two", "http://example.test/two"),
				}
			},
		},
		{
			name: "source has a blank title",
			edit: func(f *speciesFiles) {
				f.qualityReview()["verification_sources"] = []any{
					source("One", "https://example.test/one"), source("  ", "https://example.test/two"),
				}
			},
		},
		{
			name: "attempt is zero",
			edit: func(f *speciesFiles) { f.generation()["attempt"] = 0 },
		},
		{
			name: "attempt exceeds the maximum",
			edit: func(f *speciesFiles) { f.generation()["attempt"] = 4 },
		},
		{
			name: "attempt on the maximum passes",
			edit: func(f *speciesFiles) { f.generation()["attempt"] = 3 },

			accept: true,
		},
		{
			name: "correction source checksum is malformed",
			edit: func(f *speciesFiles) { f.generation()["correction_source_sha256"] = "XYZ" },
		},
		{
			name: "correction source checksum is valid",
			edit: func(f *speciesFiles) { f.generation()["correction_source_sha256"] = sum([]byte("source")) },

			accept: true,
		},
		{
			name: "unbounded generation on a current prompt",
			edit: func(f *speciesFiles) { delete(f.generation(), "max_attempts") },
		},
		{
			name: "original prompt version may lack bounded generation",
			edit: func(f *speciesFiles) {
				f.generation()["prompt_version"] = "field-journal-v1"
				delete(f.generation(), "max_attempts")
				delete(f.generation(), "attempt")
			},

			accept: true,
		},
		{
			name: "original prompt version still needs a sourced review",
			edit: func(f *speciesFiles) {
				f.generation()["prompt_version"] = "field-journal-v1"
				delete(f.generation(), "max_attempts")
				delete(f.qualityReview(), "verification_sources")
			},
		},
		{
			name: "seed image with a passing review",
			edit: func(f *speciesFiles) {
				f.generation()["generator"] = "User-approved seed image"
				f.generation()["prompt_version"] = "field-journal-seed-v1"
				delete(f.generation(), "max_attempts")
				delete(f.qualityReview(), "verification_sources")
				f.omitProfile = true
				f.omitReview = true
			},

			accept: true,
		},
		{
			name: "seed image with a low score",
			edit: func(f *speciesFiles) {
				f.generation()["generator"] = "User-approved seed image"
				delete(f.generation(), "max_attempts")
				delete(f.qualityReview(), "verification_sources")
				f.qualityReview()["text_accuracy"] = 3
			},
		},
		{
			name: "generator text alone does not waive the review",
			edit: func(f *speciesFiles) {
				f.generation()["generator"] = "User-approved seed image"
				f.qualityReview()["passed"] = false
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := build(t, []speciesFixture{wren}, func(_ string, f *speciesFiles) { tt.edit(f) })

			_, err := loadFS(t, fsys)
			if tt.accept {
				if err != nil {
					t.Fatalf("Load() error = %v, want the catalog to load", err)
				}

				return
			}

			if !errors.Is(err, catalog.ErrReviewNotPublishable) {
				t.Fatalf("Load() error = %v, want ErrReviewNotPublishable", err)
			}
		})
	}
}
