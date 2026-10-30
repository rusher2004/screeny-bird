package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// minScore is the lowest accepted value for each reviewed quality score.
	minScore = 4
	// minSources is how many distinct https sources must back a review.
	minSources = 2

	// seedGenerator and originalPromptVersion identify the explicitly
	// recognized early entries that predate bounded generation. They keep the
	// published upstream catalog valid and are not a path for new content.
	seedGenerator         = "User-approved seed image"
	originalPromptVersion = "field-journal-v1"
)

type generation struct {
	Generator              string  `json:"generator"`
	PromptVersion          string  `json:"prompt_version"`
	Attempt                int     `json:"attempt"`
	MaxAttempts            int     `json:"max_attempts"`
	CorrectionSourceSHA256 *string `json:"correction_source_sha256"`
}

type verificationSource struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type review struct {
	Passed              bool                 `json:"passed"`
	SpeciesAccuracy     int                  `json:"species_accuracy"`
	AnatomyAccuracy     int                  `json:"anatomy_accuracy"`
	TextAccuracy        int                  `json:"text_accuracy"`
	CompositionQuality  int                  `json:"composition_quality"`
	LocationFree        bool                 `json:"location_free"`
	CorrectionFindings  []json.RawMessage    `json:"correction_findings"`
	ProfileConflicts    []json.RawMessage    `json:"profile_conflicts"`
	VerificationSources []verificationSource `json:"verification_sources"`
}

// passing reports a passed, location-free review with every score at or
// above the minimum.
func (r review) passing() bool {
	return r.Passed && r.LocationFree &&
		r.SpeciesAccuracy >= minScore && r.AnatomyAccuracy >= minScore &&
		r.TextAccuracy >= minScore && r.CompositionQuality >= minScore
}

// sourced reports a passing review with no outstanding corrections or
// conflicts, backed by enough distinct https sources.
func (r review) sourced() bool {
	if !r.passing() || len(r.CorrectionFindings) > 0 || len(r.ProfileConflicts) > 0 {
		return false
	}

	urls := map[string]bool{}

	for _, source := range r.VerificationSources {
		if strings.TrimSpace(source.Title) != "" && strings.HasPrefix(source.URL, "https://") {
			urls[source.URL] = true
		}
	}

	return len(urls) >= minSources
}

// bounded reports a generation record whose attempt is within its limit and
// whose optional correction-source checksum is well formed.
func (g generation) bounded() bool {
	if g.Attempt < 1 || g.Attempt > g.MaxAttempts {
		return false
	}

	return g.CorrectionSourceSHA256 == nil || sha256Pattern.MatchString(*g.CorrectionSourceSHA256)
}

// publishable returns nil when the review and generation record satisfy one
// publication path: the current automated gates, the seed-image path, or the
// original-prompt path.
func publishable(r review, g generation) error {
	switch {
	case r.sourced() && g.bounded():
		return nil
	case g.Generator == seedGenerator && r.passing():
		return nil
	case g.PromptVersion == originalPromptVersion && r.sourced():
		return nil
	}

	return fmt.Errorf("review passed=%t, generator %q, prompt %q, attempt %d of %d: %w",
		r.Passed, g.Generator, g.PromptVersion, g.Attempt, g.MaxAttempts, ErrReviewNotPublishable)
}
