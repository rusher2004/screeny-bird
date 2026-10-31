// Package server serves the validated catalog and its images over HTTP.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/derive"
)

const (
	// maxHeaderBytes bounds request headers. It is a safety invariant, not an
	// operator setting.
	maxHeaderBytes = 8 << 10

	// schemaVersion is the catalog schema version the API serves.
	schemaVersion = 1
)

// ErrDerivedMismatch reports that the derived images and the catalog disagree
// about a species: a derived image for one the catalog lacks, or under another
// slug.
var ErrDerivedMismatch = errors.New("derived images do not match the catalog")

// ErrInvalidServer reports a server built without a required option or with an
// unusable one.
var ErrInvalidServer = errors.New("invalid server")

// store is what the server needs from the catalog on disk.
type store interface {
	// Load validates and returns the whole catalog.
	Load() (*catalog.Catalog, error)
	// IndexVersion returns a token that changes whenever the catalog's index
	// is replaced, so a change can be noticed without loading.
	IndexVersion() (string, error)
	// ReadAsset returns the bytes of one file inside the catalog directory.
	ReadAsset(name string) ([]byte, error)
}

// derivedStore is what the server needs from the derived images on disk.
type derivedStore interface {
	// Load returns the validated derived index.
	Load() (derive.Index, error)
	// IndexVersion returns a token that changes whenever the derived index is
	// replaced.
	IndexVersion() (string, error)
	// ReadAsset returns the bytes of one file inside the derived directory.
	ReadAsset(name string) ([]byte, error)
}

// listener opens the socket the server accepts connections on.
type listener interface {
	Listen() (net.Listener, error)
}

// ticker delivers a signal every interval until stop is called.
type ticker interface {
	Tick(every time.Duration) (ticks <-chan struct{}, stop func())
}

// accessLog records one finished request.
type accessLog interface {
	Log(method, path string, status, bytes int)
}

// Options is everything a server depends on. Every field is required.
type Options struct {
	Store    store
	Derived  derivedStore
	Listener listener
	Ticker   ticker
	Log      accessLog
	// Version is the application version /healthz reports.
	Version string
	// Spec is the OpenAPI document served at /v1/openapi.yaml.
	Spec []byte
	// AllowedOrigins are the browser origins allowed to read the catalog and
	// images. An empty, non-nil list allows none.
	AllowedOrigins    []string
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	ShutdownTimeout   time.Duration
	ReloadInterval    time.Duration
}

// Server serves one catalog. Build it with New and run it with Serve.
type Server struct {
	opts     Options
	allowed  map[string]bool
	specETag string

	// state is the catalog being served. It is replaced whole, never edited.
	state atomic.Pointer[snapshot]
	// reloadFailing is true while the latest reload failed and the server is
	// still serving the last catalog it validated.
	reloadFailing atomic.Bool
	// attempted is the index version the last reload looked at. Only the
	// serving loop touches it.
	attempted string
}

// New returns a server that has every option it needs. It reports every
// problem together.
func New(opts Options) (*Server, error) {
	var problems []error

	for _, dep := range []struct {
		name    string
		missing bool
	}{
		{"store", opts.Store == nil},
		{"derived store", opts.Derived == nil},
		{"listener", opts.Listener == nil},
		{"ticker", opts.Ticker == nil},
		{"log", opts.Log == nil},
		{"version", strings.TrimSpace(opts.Version) == ""},
		{"spec", len(opts.Spec) == 0},
		{"allowed origins", opts.AllowedOrigins == nil},
	} {
		if dep.missing {
			problems = append(problems, fmt.Errorf("%s is required", dep.name))
		}
	}

	for _, d := range []struct {
		name  string
		value time.Duration
	}{
		{"read header timeout", opts.ReadHeaderTimeout},
		{"write timeout", opts.WriteTimeout},
		{"shutdown timeout", opts.ShutdownTimeout},
		{"reload interval", opts.ReloadInterval},
	} {
		if d.value <= 0 {
			problems = append(problems, fmt.Errorf("%s must be positive", d.name))
		}
	}

	allowed := make(map[string]bool, len(opts.AllowedOrigins))

	for _, origin := range opts.AllowedOrigins {
		if strings.TrimSpace(origin) == "" || strings.Contains(origin, "*") {
			problems = append(problems, fmt.Errorf("allowed origin %q must be an exact origin, never a wildcard", origin))

			continue
		}

		allowed[origin] = true
	}

	if err := errors.Join(problems...); err != nil {
		return nil, fmt.Errorf("creating server: %w: %w", ErrInvalidServer, err)
	}

	digest := sha256.Sum256(opts.Spec)

	return &Server{opts: opts, allowed: allowed, specETag: `"` + hex.EncodeToString(digest[:]) + `"`}, nil
}

// snapshot is one validated catalog, ready to serve.
type snapshot struct {
	indexVersion string
	// species is how many species are served: those with a derived image.
	species int
	// pending is how many catalog species are not served yet because derive
	// has not made their image.
	pending     int
	generatedAt string
	catalogJSON []byte
	catalogETag string
	// assets maps each served asset path to the SHA-256 the catalog declares.
	assets map[string]string
	// derived maps each served derived image path to its recorded SHA-256.
	derived map[string]string
}

// catalogResponse is the JSON body of /v1/catalog. The field order is the
// sorted key order.
type catalogResponse struct {
	GeneratedAt   *string         `json:"generated_at"`
	SchemaVersion int             `json:"schema_version"`
	Species       []servedSpecies `json:"species"`
}

// servedSpecies is one species as the API serves it: the catalog's fields and
// its derived image. The field order is the sorted key order.
type servedSpecies struct {
	ApprovedAt     string        `json:"approved_at"`
	CommonName     string        `json:"common_name"`
	Derived        servedDerived `json:"derived"`
	DisplayPath    string        `json:"display_path"`
	DisplaySHA256  string        `json:"display_sha256"`
	PortraitPath   string        `json:"portrait_path"`
	PortraitSHA256 string        `json:"portrait_sha256"`
	ScientificName string        `json:"scientific_name"`
	Slug           string        `json:"slug"`
	TaxonID        int           `json:"taxon_id"`
}

// servedDerived is a species' screensaver-sized image.
type servedDerived struct {
	Height int    `json:"height"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Width  int    `json:"width"`
}

// joinDerived pairs every catalog species with its derived image. A species
// derive has not reached yet is left out and counted as pending, so it is
// served as soon as its image exists. A derived image for a species the
// catalog lacks, or under a different slug, means the two disagree about what
// exists, and every such mismatch is reported.
func joinDerived(species []catalog.Species, idx derive.Index) (served []servedSpecies, pending int, err error) {
	byTaxon := make(map[int]derive.Entry, len(idx.Species))
	for _, e := range idx.Species {
		byTaxon[e.TaxonID] = e
	}

	served = make([]servedSpecies, 0, len(species))

	var problems []error

	for _, sp := range species {
		e, ok := byTaxon[sp.TaxonID]
		if !ok {
			pending++

			continue
		}

		delete(byTaxon, sp.TaxonID)

		if e.Slug != sp.Slug {
			problems = append(problems, fmt.Errorf("derived image %s is for %q but the catalog names taxon %d %q: %w", e.Path, e.Slug, sp.TaxonID, sp.Slug, ErrDerivedMismatch))

			continue
		}

		served = append(served, servedSpecies{
			ApprovedAt:     sp.ApprovedAt,
			CommonName:     sp.CommonName,
			Derived:        servedDerived{Height: idx.Height, Path: e.Path, SHA256: e.SHA256, Width: idx.Width},
			DisplayPath:    sp.DisplayPath,
			DisplaySHA256:  sp.DisplaySHA256,
			PortraitPath:   sp.PortraitPath,
			PortraitSHA256: sp.PortraitSHA256,
			ScientificName: sp.ScientificName,
			Slug:           sp.Slug,
			TaxonID:        sp.TaxonID,
		})
	}

	for taxon, e := range byTaxon {
		problems = append(problems, fmt.Errorf("derived image %s is for taxon %d, which the catalog lacks: %w", e.Path, taxon, ErrDerivedMismatch))
	}

	if err := errors.Join(problems...); err != nil {
		return nil, 0, err
	}

	return served, pending, nil
}

// indexVersion is the token that changes when either index is replaced.
func (s *Server) indexVersion() (string, error) {
	catalogVersion, err := s.opts.Store.IndexVersion()
	if err != nil {
		return "", fmt.Errorf("reading the index version: %w", err)
	}

	derivedVersion, err := s.opts.Derived.IndexVersion()
	if err != nil {
		return "", fmt.Errorf("reading the derived index version: %w", err)
	}

	return catalogVersion + "|" + derivedVersion, nil
}

// load validates the catalog and the derived index and builds a snapshot of
// them. It reads the index versions first, so a change that lands during the
// load is noticed next time.
func (s *Server) load() (*snapshot, error) {
	version, err := s.indexVersion()
	if err != nil {
		return nil, err
	}

	loaded, err := s.opts.Store.Load()
	if err != nil {
		return nil, fmt.Errorf("loading the catalog: %w", err)
	}

	idx, err := s.opts.Derived.Load()
	if err != nil {
		return nil, fmt.Errorf("loading the derived index: %w", err)
	}

	served, pending, err := joinDerived(loaded.Species(), idx)
	if err != nil {
		return nil, err
	}

	response := catalogResponse{SchemaVersion: schemaVersion, Species: served}

	if latest, ok := loaded.GeneratedAt(); ok {
		response.GeneratedAt = &latest
	}

	body, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encoding the catalog: %w", err)
	}

	digest := sha256.Sum256(body)
	snap := &snapshot{
		indexVersion: version,
		species:      len(served),
		pending:      pending,
		catalogJSON:  body,
		catalogETag:  `"` + hex.EncodeToString(digest[:]) + `"`,
		assets:       make(map[string]string, 2*len(served)),
		derived:      make(map[string]string, len(served)),
	}

	if response.GeneratedAt != nil {
		snap.generatedAt = *response.GeneratedAt
	}

	for _, sp := range served {
		snap.assets[sp.PortraitPath] = sp.PortraitSHA256
		snap.assets[sp.DisplayPath] = sp.DisplaySHA256
		snap.derived[sp.Derived.Path] = sp.Derived.SHA256
	}

	return snap, nil
}

// Serve loads and validates the catalog, then serves it until ctx is done. A
// catalog that does not validate stops the server before it listens. While
// serving, it reloads a changed catalog on every tick and keeps serving the
// last validated one if a reload fails. A clean shutdown returns nil.
func (s *Server) Serve(ctx context.Context) error {
	snap, err := s.load()
	if err != nil {
		return fmt.Errorf("preparing to serve: %w", err)
	}

	s.state.Store(snap)
	s.attempted = snap.indexVersion

	ln, err := s.opts.Listener.Listen()
	if err != nil {
		return fmt.Errorf("opening the listener: %w", err)
	}

	srv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		WriteTimeout:      s.opts.WriteTimeout,
		IdleTimeout:       s.opts.ReadHeaderTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		// The server never prints; main owns the output.
		ErrorLog: log.New(io.Discard, "", 0),
	}

	served := make(chan error, 1)

	go func() { served <- srv.Serve(ln) }()

	ticks, stop := s.opts.Ticker.Tick(s.opts.ReloadInterval)
	defer stop()

	for {
		select {
		case <-ticks:
			s.reload()
		case err := <-served:
			return fmt.Errorf("serving: %w", err)
		case <-ctx.Done():
			return s.shutdown(ctx, srv, served)
		}
	}
}

// shutdown stops accepting, lets in-flight requests finish within the
// configured time, and reports anything that went wrong.
func (s *Server) shutdown(ctx context.Context, srv *http.Server, served <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.opts.ShutdownTimeout)
	defer cancel()

	var errs []error

	if err := srv.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("shutting down: %w", err))

		if closeErr := srv.Close(); closeErr != nil {
			errs = append(errs, fmt.Errorf("closing the server: %w", closeErr))
		}
	}

	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs = append(errs, fmt.Errorf("serving: %w", err))
	}

	return errors.Join(errs...)
}

// reload swaps in a changed catalog. It does nothing while the index is the
// one it last looked at, and keeps the old catalog if the new one is invalid.
func (s *Server) reload() {
	version, err := s.indexVersion()
	if err != nil {
		s.reloadFailing.Store(true)

		return
	}

	if version == s.attempted {
		return
	}

	s.attempted = version

	snap, err := s.load()
	if err != nil {
		s.reloadFailing.Store(true)

		return
	}

	s.state.Store(snap)
	s.reloadFailing.Store(false)
}
