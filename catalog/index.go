package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
)

// index is the catalog's public listing, the content of index.json. The field
// order is the sorted JSON key order.
type index struct {
	// GeneratedAt is the latest approval time, or nil for an empty catalog.
	GeneratedAt   *string   `json:"generated_at"`
	SchemaVersion int       `json:"schema_version"`
	Species       []Species `json:"species"`
}

// indexOf returns the index that the sorted species' manifests produce.
func indexOf(species []Species) index {
	listing := index{SchemaVersion: schemaVersion, Species: slices.Clone(species)}

	for _, s := range species {
		if listing.GeneratedAt == nil || s.ApprovedAt > *listing.GeneratedAt {
			approved := s.ApprovedAt
			listing.GeneratedAt = &approved
		}
	}

	return listing
}

// checkIndex confirms index.json is exactly the projection of the manifests.
func checkIndex(fsys fs.FS, want index) error {
	raw, err := readFile(fsys, indexFile)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrIndexMismatch, err)
	}

	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return fmt.Errorf("decoding: %w: %w", ErrIndexMismatch, err)
	}

	if err := scanPrivacy(generic, indexFile); err != nil {
		return err
	}

	// Unknown fields mean the index exposes more than the documented projection.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var got index
	if err := decoder.Decode(&got); err != nil {
		return fmt.Errorf("decoding: %w: %w", ErrIndexMismatch, err)
	}

	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("data after the index object: %w", ErrIndexMismatch)
	}

	if got.SchemaVersion != schemaVersion {
		return fmt.Errorf("schema_version %d: %w: %w", got.SchemaVersion, ErrUnsupportedSchema, ErrIndexMismatch)
	}

	if !sameTime(got.GeneratedAt, want.GeneratedAt) {
		return fmt.Errorf("generated_at is %s, want %s: %w", showTime(got.GeneratedAt), showTime(want.GeneratedAt), ErrIndexMismatch)
	}

	if len(got.Species) != len(want.Species) {
		return fmt.Errorf("lists %d species, manifests have %d: %w", len(got.Species), len(want.Species), ErrIndexMismatch)
	}

	for n := range want.Species {
		if got.Species[n] != want.Species[n] {
			return fmt.Errorf("entry %d (want taxon %d) differs from its manifest: %w", n, want.Species[n].TaxonID, ErrIndexMismatch)
		}
	}

	return nil
}

func sameTime(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

func showTime(t *string) string {
	if t == nil {
		return "null"
	}

	return *t
}
