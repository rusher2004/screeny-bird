// Package mirror keeps a local catalog in step with an upstream one. A sync
// only ever adds species the local store lacks: a species the store already
// holds is never replaced, and a sync that finds one changed upstream stops and
// reports it. Downloads are staged per upstream commit and verified, so a sync
// that fails partway resumes from what it already has instead of starting over.
package mirror

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/fsutil"
	"github.com/rusher2004/screeny-bird/upstream"
)

const (
	// upstreamCatalog is where the catalog lives inside the upstream repository.
	upstreamCatalog = "catalog"
	indexFile       = "index.json"
	provenanceFile  = "upstream.json"
	lockFile        = "sync.lock"
	manifestFile    = "manifest.json"
	portraitFile    = "portrait.png"
	displayFile     = "display.png"
	schemaVersion   = 1

	// stagePrefix names the staging directories in the work directory; the
	// commit being synced follows it.
	stagePrefix = "stage-"
	// partialSuffix marks a species download that has not finished.
	partialSuffix = ".partial"
)

var (
	// ErrInvalidSyncer reports a syncer built without a required argument.
	ErrInvalidSyncer = errors.New("invalid syncer")
	// ErrInvalidUpstreamIndex reports an upstream index, or an upstream species
	// that disagrees with it, that cannot be trusted.
	ErrInvalidUpstreamIndex = errors.New("invalid upstream index")
	// ErrUpstreamChanged reports a species the store holds that differs upstream.
	ErrUpstreamChanged = errors.New("upstream changed a species the store already holds")
	// ErrAssetMismatch reports a downloaded file whose SHA-256 differs from the
	// upstream index.
	ErrAssetMismatch = errors.New("downloaded file does not match the upstream index")
	// ErrInvalidCommit reports an upstream ref that did not resolve to a full
	// commit SHA, which staging directories are named after.
	ErrInvalidCommit = errors.New("invalid upstream commit")
)

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

	requiredFiles = []string{manifestFile, portraitFile, displayFile}
	optionalFiles = []string{"profile.json", "quality-review.json"}
)

// source is what a syncer needs from the upstream repository.
type source interface {
	ResolveRef(ctx context.Context) (string, error)
	Fetch(ctx context.Context, commit, filePath string) ([]byte, error)
}

// progress is told how a sync is going, so whoever runs it can show progress.
// The syncer itself never logs.
type progress interface {
	Resolved(commit string)
	// Planned reports how many species upstream has and how many the store lacks.
	Planned(upstream, missing int)
	// Staged reports that done of total species are staged; reused says the
	// species was already staged and verified by an earlier run.
	Staged(done, total int, species string, reused bool)
	Validating()
	Publishing()
}

// Syncer adds the species an upstream catalog has and a local store lacks.
type Syncer struct {
	src      source
	progress progress
	repo     string
	ref      string
	storeDir string
	workDir  string
}

// NewSyncer returns a syncer that mirrors src into the catalog in storeDir.
// repo and ref only label the provenance record. workDir holds staging and
// state, and must be on the same filesystem as storeDir so publishing is a
// rename. Every argument is required, and all problems are reported together.
func NewSyncer(src source, progress progress, repo, ref, storeDir, workDir string) (Syncer, error) {
	var problems []error

	for _, arg := range []struct {
		name  string
		blank bool
	}{
		{"source", src == nil},
		{"progress", progress == nil},
		{"repo", strings.TrimSpace(repo) == ""},
		{"ref", strings.TrimSpace(ref) == ""},
		{"store directory", strings.TrimSpace(storeDir) == ""},
		{"work directory", strings.TrimSpace(workDir) == ""},
	} {
		if arg.blank {
			problems = append(problems, fmt.Errorf("%s is required", arg.name))
		}
	}

	if err := errors.Join(problems...); err != nil {
		return Syncer{}, fmt.Errorf("creating syncer: %w: %w", ErrInvalidSyncer, err)
	}

	return Syncer{src: src, progress: progress, repo: repo, ref: ref, storeDir: storeDir, workDir: workDir}, nil
}

// entry is one species in the upstream index.
type entry struct {
	ApprovedAt     string `json:"approved_at"`
	CommonName     string `json:"common_name"`
	DisplayPath    string `json:"display_path"`
	DisplaySHA256  string `json:"display_sha256"`
	PortraitPath   string `json:"portrait_path"`
	PortraitSHA256 string `json:"portrait_sha256"`
	ScientificName string `json:"scientific_name"`
	Slug           string `json:"slug"`
	TaxonID        int    `json:"taxon_id"`
}

func (e entry) dir() string {
	return fmt.Sprintf("%d-%s", e.TaxonID, e.Slug)
}

type upstreamIndex struct {
	GeneratedAt   *string `json:"generated_at"`
	SchemaVersion int     `json:"schema_version"`
	Species       []entry `json:"species"`
}

// Sync reads the upstream ref once, as one commit, and adds every species the
// store lacks. It returns that commit and how many species it added. Either
// every new species is published together or none is.
func (s Syncer) Sync(ctx context.Context) (commit string, added int, err error) {
	if err := ctx.Err(); err != nil {
		return "", 0, fmt.Errorf("starting sync: %w", err)
	}

	if err := os.MkdirAll(s.workDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("creating work directory %s: %w", s.workDir, err)
	}

	lock, err := fsutil.AcquireLock(filepath.Join(s.workDir, lockFile))
	if err != nil {
		return "", 0, fmt.Errorf("starting sync: %w", err)
	}

	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("releasing the sync lock: %w", releaseErr))
		}
	}()

	commit, err = s.src.ResolveRef(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("resolving the upstream ref: %w", err)
	}

	if !commitPattern.MatchString(commit) {
		return "", 0, fmt.Errorf("the upstream ref resolved to %q: %w", commit, ErrInvalidCommit)
	}

	s.progress.Resolved(commit)

	if err := s.removeOtherStaging(commit); err != nil {
		return "", 0, err
	}

	entries, err := s.fetchIndex(ctx, commit)
	if err != nil {
		return "", 0, err
	}

	missing, err := s.findMissing(entries)
	if err != nil {
		return "", 0, err
	}

	s.progress.Planned(len(entries), len(missing))

	if len(missing) > 0 {
		if err := s.addSpecies(ctx, commit, missing); err != nil {
			return "", 0, err
		}
	}

	if err := s.recordProvenance(commit, len(missing)); err != nil {
		return "", 0, err
	}

	return commit, len(missing), nil
}

func (s Syncer) fetchIndex(ctx context.Context, commit string) ([]entry, error) {
	raw, err := s.src.Fetch(ctx, commit, path.Join(upstreamCatalog, indexFile))
	if err != nil {
		return nil, fmt.Errorf("fetching the upstream index: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var index upstreamIndex
	if err := decoder.Decode(&index); err != nil {
		return nil, fmt.Errorf("decoding the upstream index: %w: %w", ErrInvalidUpstreamIndex, err)
	}

	if index.SchemaVersion != schemaVersion {
		return nil, fmt.Errorf("upstream index schema_version %d: %w", index.SchemaVersion, ErrInvalidUpstreamIndex)
	}

	seen := map[int]bool{}

	for _, e := range index.Species {
		if err := checkEntry(e); err != nil {
			return nil, fmt.Errorf("upstream index species %q: %w: %w", e.dir(), ErrInvalidUpstreamIndex, err)
		}

		if seen[e.TaxonID] {
			return nil, fmt.Errorf("upstream index lists taxon %d twice: %w", e.TaxonID, ErrInvalidUpstreamIndex)
		}

		seen[e.TaxonID] = true
	}

	return index.Species, nil
}

func checkEntry(e entry) error {
	switch {
	case e.TaxonID <= 0:
		return errors.New("taxon_id must be positive")
	case !catalog.ValidSlug(e.Slug):
		return fmt.Errorf("slug %q is not a slug", e.Slug)
	case e.PortraitPath != path.Join("species", e.dir(), portraitFile):
		return fmt.Errorf("portrait_path %q is not this species' portrait", e.PortraitPath)
	case e.DisplayPath != path.Join("species", e.dir(), displayFile):
		return fmt.Errorf("display_path %q is not this species' display image", e.DisplayPath)
	case !catalog.ValidSHA256(e.PortraitSHA256) || !catalog.ValidSHA256(e.DisplaySHA256):
		return errors.New("checksums must be lowercase hex SHA-256")
	}

	return nil
}

// localManifest is the part of a stored manifest sync compares.
type localManifest struct {
	Assets map[string]struct {
		SHA256 string `json:"sha256"`
	} `json:"assets"`
}

// findMissing returns the upstream species the store lacks. It fails, listing
// every one, if the store holds a species that differs upstream.
func (s Syncer) findMissing(entries []entry) ([]entry, error) {
	local, err := s.localDirs()
	if err != nil {
		return nil, err
	}

	var (
		missing []entry
		changed []error
	)

	for _, e := range entries {
		if local[e.dir()] {
			differs, err := s.differsFromStore(e)
			if err != nil {
				return nil, err
			}

			if differs {
				changed = append(changed, fmt.Errorf("%s: checksums differ upstream: %w", e.dir(), ErrUpstreamChanged))
			}

			continue
		}

		if held, ok := holdsTaxon(local, e.TaxonID); ok {
			changed = append(changed, fmt.Errorf("taxon %d is stored as %s but listed upstream as %s: %w", e.TaxonID, held, e.dir(), ErrUpstreamChanged))

			continue
		}

		missing = append(missing, e)
	}

	if err := errors.Join(changed...); err != nil {
		return nil, fmt.Errorf("refusing to sync: %w", err)
	}

	return missing, nil
}

// holdsTaxon returns the stored directory for a taxon ID, if any.
func holdsTaxon(local map[string]bool, taxonID int) (string, bool) {
	prefix := fmt.Sprintf("%d-", taxonID)

	for dir := range local {
		if strings.HasPrefix(dir, prefix) {
			return dir, true
		}
	}

	return "", false
}

func (s Syncer) localDirs() (map[string]bool, error) {
	entries, err := os.ReadDir(filepath.Join(s.storeDir, "species"))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the store's species: %w", err)
	}

	local := make(map[string]bool, len(entries))

	for _, entry := range entries {
		local[entry.Name()] = true
	}

	return local, nil
}

func (s Syncer) differsFromStore(e entry) (bool, error) {
	manifestPath := filepath.Join(s.storeDir, "species", e.dir(), manifestFile)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return false, fmt.Errorf("reading the stored manifest of %s: %w", e.dir(), err)
	}

	var stored localManifest
	if err := json.Unmarshal(raw, &stored); err != nil {
		return false, fmt.Errorf("decoding the stored manifest of %s: %w", e.dir(), err)
	}

	return stored.Assets["portrait"].SHA256 != e.PortraitSHA256 || stored.Assets["display"].SHA256 != e.DisplaySHA256, nil
}

// stageDir is where one commit's downloads wait between runs.
func (s Syncer) stageDir(commit string) string {
	return filepath.Join(s.workDir, stagePrefix+commit)
}

// removeOtherStaging deletes the staging of every other commit, so old attempts
// never pile up. Staging for the commit being synced is kept: it holds the
// verified downloads a retry resumes from.
func (s Syncer) removeOtherStaging(commit string) error {
	entries, err := os.ReadDir(s.workDir)
	if err != nil {
		return fmt.Errorf("reading the work directory %s: %w", s.workDir, err)
	}

	var errs []error

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, stagePrefix) || name == stagePrefix+commit {
			continue
		}

		if err := os.RemoveAll(filepath.Join(s.workDir, name)); err != nil {
			errs = append(errs, fmt.Errorf("removing stale staging %s: %w", name, err))
		}
	}

	return errors.Join(errs...)
}

// addSpecies stages, validates and publishes the missing species. A failure
// keeps the species already staged and verified, so the next run resumes from
// them. Only a successful publish removes the staging.
func (s Syncer) addSpecies(ctx context.Context, commit string, missing []entry) error {
	stage := s.stageDir(commit)
	speciesRoot := filepath.Join(stage, "species")

	if err := os.MkdirAll(speciesRoot, 0o755); err != nil {
		return fmt.Errorf("creating the staging directory: %w", err)
	}

	if err := dropUnwantedStaging(speciesRoot, missing); err != nil {
		return err
	}

	for i, e := range missing {
		reused, err := s.stageSpecies(ctx, commit, speciesRoot, e)
		if err != nil {
			return err
		}

		s.progress.Staged(i+1, len(missing), e.dir(), reused)
	}

	s.progress.Validating()

	if err := validateStage(stage, missing); err != nil {
		return err
	}

	s.progress.Publishing()

	if err := s.publish(stage, missing); err != nil {
		return err
	}

	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("the catalog was updated, but removing the staging directory %s failed: %w", stage, err)
	}

	return nil
}

// dropUnwantedStaging removes staged entries that are not a species this run
// needs: unfinished downloads, and species the store has gained since.
func dropUnwantedStaging(speciesRoot string, missing []entry) error {
	wanted := make(map[string]bool, len(missing))
	for _, e := range missing {
		wanted[e.dir()] = true
	}

	entries, err := os.ReadDir(speciesRoot)
	if err != nil {
		return fmt.Errorf("reading the staged species: %w", err)
	}

	var errs []error

	for _, entry := range entries {
		if wanted[entry.Name()] {
			continue
		}

		if err := os.RemoveAll(filepath.Join(speciesRoot, entry.Name())); err != nil {
			errs = append(errs, fmt.Errorf("removing unneeded staged %s: %w", entry.Name(), err))
		}
	}

	return errors.Join(errs...)
}

// stageSpecies makes sure one species is staged and verified. A species staged
// by an earlier run is reused if its files still match the upstream index;
// otherwise it is downloaded again into an unfinished directory that becomes
// the species only once every file is written. It reports whether the staged
// copy was reused.
func (s Syncer) stageSpecies(ctx context.Context, commit, speciesRoot string, e entry) (reused bool, err error) {
	final := filepath.Join(speciesRoot, e.dir())

	_, statErr := os.Stat(final)

	switch {
	case statErr == nil:
		if verifyStaged(final, e) == nil {
			return true, nil
		}

		if err := os.RemoveAll(final); err != nil {
			return false, fmt.Errorf("removing the stale staged %s: %w", e.dir(), err)
		}
	case !errors.Is(statErr, fs.ErrNotExist):
		return false, fmt.Errorf("inspecting the staged %s: %w", e.dir(), statErr)
	}

	partial := final + partialSuffix

	if err := os.RemoveAll(partial); err != nil {
		return false, fmt.Errorf("removing the unfinished download of %s: %w", e.dir(), err)
	}

	if err := s.download(ctx, commit, partial, e); err != nil {
		return false, errors.Join(err, os.RemoveAll(partial))
	}

	if err := os.Rename(partial, final); err != nil {
		return false, fmt.Errorf("finishing the download of %s: %w", e.dir(), err)
	}

	return false, nil
}

// verifyStaged reports whether a staged species still has its manifest and
// both images, and the images still match the upstream index.
func verifyStaged(dir string, e entry) error {
	if _, err := os.Stat(filepath.Join(dir, manifestFile)); err != nil {
		return fmt.Errorf("staged manifest: %w", err)
	}

	for name, want := range map[string]string{portraitFile: e.PortraitSHA256, displayFile: e.DisplaySHA256} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("staged %s: %w", name, err)
		}

		if got := sha256Hex(data); got != want {
			return fmt.Errorf("staged %s has SHA-256 %s, the index says %s: %w", name, got, want, ErrAssetMismatch)
		}
	}

	return nil
}

// download fetches one species' files into dir.
func (s Syncer) download(ctx context.Context, commit, dir string, e entry) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("downloading %s: %w", e.dir(), err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating staging directory for %s: %w", e.dir(), err)
	}

	for _, name := range append(append([]string{}, requiredFiles...), optionalFiles...) {
		data, err := s.src.Fetch(ctx, commit, path.Join(upstreamCatalog, "species", e.dir(), name))

		switch {
		case errors.Is(err, upstream.ErrNotFound) && contains(optionalFiles, name):
			continue
		case err != nil:
			return fmt.Errorf("downloading %s of %s: %w", name, e.dir(), err)
		}

		if err := checkDownload(e, name, data); err != nil {
			return err
		}

		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return fmt.Errorf("writing %s of %s: %w", name, e.dir(), err)
		}
	}

	return nil
}

func contains(names []string, name string) bool {
	return slices.Contains(names, name)
}

// checkDownload verifies the images against the upstream index.
func checkDownload(e entry, name string, data []byte) error {
	var want string

	switch name {
	case portraitFile:
		want = e.PortraitSHA256
	case displayFile:
		want = e.DisplaySHA256
	default:
		return nil
	}

	if got := sha256Hex(data); got != want {
		return fmt.Errorf("%s of %s has SHA-256 %s, the index says %s: %w", name, e.dir(), got, want, ErrAssetMismatch)
	}

	return nil
}

// validateStage runs the catalog rules on everything staged and confirms each
// staged species agrees with its upstream index entry.
func validateStage(stage string, missing []entry) error {
	loader, err := catalog.NewDirLoader(stage)
	if err != nil {
		return fmt.Errorf("creating staging loader: %w", err)
	}

	loaded, err := loader.RebuildIndex()
	if err != nil {
		return fmt.Errorf("validating downloaded species: %w", err)
	}

	byTaxon := make(map[int]entry, len(missing))
	for _, e := range missing {
		byTaxon[e.TaxonID] = e
	}

	for _, got := range loaded.Species() {
		want, ok := byTaxon[got.TaxonID]
		if !ok || got.CommonName != want.CommonName || got.ScientificName != want.ScientificName ||
			got.Slug != want.Slug || got.ApprovedAt != want.ApprovedAt ||
			got.PortraitSHA256 != want.PortraitSHA256 || got.DisplaySHA256 != want.DisplaySHA256 {
			return fmt.Errorf("manifest of taxon %d disagrees with the upstream index: %w", got.TaxonID, ErrInvalidUpstreamIndex)
		}
	}

	if len(loaded.Species()) != len(missing) {
		return fmt.Errorf("downloaded %d species, the index listed %d: %w", len(loaded.Species()), len(missing), ErrInvalidUpstreamIndex)
	}

	return nil
}

// publish moves the staged species into the store and rebuilds the store's
// index. If any step fails, the species already moved are removed again and the
// store's index is left as it was.
func (s Syncer) publish(stage string, missing []entry) error {
	speciesRoot := filepath.Join(s.storeDir, "species")
	if err := os.MkdirAll(speciesRoot, 0o755); err != nil {
		return fmt.Errorf("creating the store's species directory: %w", err)
	}

	var moved []string

	rollback := func(cause error) error {
		errs := []error{cause}

		for _, dir := range moved {
			if err := os.RemoveAll(dir); err != nil {
				errs = append(errs, fmt.Errorf("rolling back %s: %w", dir, err))
			}
		}

		return errors.Join(errs...)
	}

	for _, e := range missing {
		target := filepath.Join(speciesRoot, e.dir())

		if err := os.Rename(filepath.Join(stage, "species", e.dir()), target); err != nil {
			return rollback(fmt.Errorf("publishing %s: %w", e.dir(), err))
		}

		moved = append(moved, target)
	}

	loader, err := catalog.NewDirLoader(s.storeDir)
	if err != nil {
		return rollback(fmt.Errorf("creating store loader: %w", err))
	}

	if _, err := loader.RebuildIndex(); err != nil {
		return rollback(fmt.Errorf("rebuilding the store's index: %w", err))
	}

	return nil
}

type provenance struct {
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
	Added  int    `json:"added"`
}

func (s Syncer) recordProvenance(commit string, added int) error {
	data, err := json.MarshalIndent(provenance{Repo: s.repo, Ref: s.ref, Commit: commit, Added: added}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the provenance record: %w", err)
	}

	if err := fsutil.WriteFileAtomic(filepath.Join(s.workDir, provenanceFile), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("recording provenance: %w", err)
	}

	return nil
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)

	return hex.EncodeToString(digest[:])
}
