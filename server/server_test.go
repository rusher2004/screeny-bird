package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rusher2004/screeny-bird/catalog"
	"github.com/rusher2004/screeny-bird/server"
)

// Species in ../testdata/species, by directory name.
const (
	wren    = "1001-carolina-wren"
	finch   = "1002-house-finch"
	bunting = "1003-painted-bunting"
)

// pipeListener is an in-memory net.Listener: clients reach it through dial, so
// no operating-system socket is ever opened.
type pipeListener struct {
	conns  chan net.Conn
	done   chan struct{}
	once   sync.Once
	closed atomic.Bool
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() {
		l.closed.Store(true)
		close(l.done)
	})

	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, serverSide := net.Pipe()

	select {
	case l.conns <- serverSide:
		return client, nil
	case <-l.done:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// listenerProvider hands the server one pipe listener, or an error.
type listenerProvider struct {
	listener *pipeListener
	err      error
	calls    atomic.Int32
}

func (p *listenerProvider) Listen() (net.Listener, error) {
	p.calls.Add(1)

	if p.err != nil {
		return nil, p.err
	}

	return p.listener, nil
}

// fakeTicker lets a test trigger a reload by hand.
type fakeTicker struct {
	ticks chan struct{}
	every atomic.Int64
}

func (f *fakeTicker) Tick(every time.Duration) (<-chan struct{}, func()) {
	f.every.Store(int64(every))

	return f.ticks, func() {}
}

func (f *fakeTicker) fire() { f.ticks <- struct{}{} }

type logEntry struct {
	method, path string
	status       int
	bytes        int
}

type fakeLog struct {
	mu      sync.Mutex
	entries []logEntry
}

func (f *fakeLog) Log(method, path string, status, bytes int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.entries = append(f.entries, logEntry{method, path, status, bytes})
}

// reset forgets what was logged so far, such as the start-up readiness probe.
func (f *fakeLog) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.entries = nil
}

func (f *fakeLog) all() []logEntry {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.entries)
}

// countingStore wraps the real directory loader and counts its calls.
type countingStore struct {
	inner  catalog.DirLoader
	loads  atomic.Int32
	reads  atomic.Int32
	checks atomic.Int32
}

func (c *countingStore) Load() (*catalog.Catalog, error) {
	c.loads.Add(1)

	return c.inner.Load()
}

func (c *countingStore) IndexVersion() (string, error) {
	c.checks.Add(1)

	return c.inner.IndexVersion()
}

func (c *countingStore) ReadAsset(name string) ([]byte, error) {
	c.reads.Add(1)

	return c.inner.ReadAsset(name)
}

// copyTree copies a directory of regular files.
func copyTree(t *testing.T, from, to string) {
	t.Helper()

	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(from, path)
		if relErr != nil {
			return relErr
		}

		target := filepath.Join(to, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copying %s: %v", from, err)
	}
}

// newStoreDir builds a catalog directory from the fixture species.
func newStoreDir(t *testing.T, species ...string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "species"), 0o755); err != nil {
		t.Fatalf("creating species directory: %v", err)
	}

	for _, name := range species {
		addSpecies(t, dir, name)
	}

	rebuild(t, dir)

	return dir
}

func addSpecies(t *testing.T, dir, name string) {
	t.Helper()

	target := filepath.Join(dir, "species", name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}

	copyTree(t, filepath.Join("..", "testdata", "species", name), target)
}

func rebuild(t *testing.T, dir string) {
	t.Helper()

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	if _, err := loader.RebuildIndex(); err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}
}

// harness runs a server against a real directory over in-memory pipes.
type harness struct {
	t         *testing.T
	dir       string
	store     *countingStore
	provider  *listenerProvider
	ticker    *fakeTicker
	log       *fakeLog
	client    *http.Client
	cancel    context.CancelFunc
	done      chan error
	opts      server.Options
	serveOnce sync.Once
}

// newHarness prepares a server but does not start it.
func newHarness(t *testing.T, origins []string, species ...string) *harness {
	t.Helper()

	dir := newStoreDir(t, species...)

	loader, err := catalog.NewDirLoader(dir)
	if err != nil {
		t.Fatalf("NewDirLoader() error = %v", err)
	}

	pipe := newPipeListener()
	h := &harness{
		t:        t,
		dir:      dir,
		store:    &countingStore{inner: loader},
		provider: &listenerProvider{listener: pipe},
		ticker:   &fakeTicker{ticks: make(chan struct{}, 4)},
		log:      &fakeLog{},
		done:     make(chan error, 1),
		client: &http.Client{
			Transport: &http.Transport{DialContext: pipe.dial, DisableKeepAlives: true},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: 10 * time.Second,
		},
	}

	h.opts = server.Options{
		Store:             h.store,
		Listener:          h.provider,
		Ticker:            h.ticker,
		Log:               h.log,
		Version:           "9.9.9",
		AllowedOrigins:    origins,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		ShutdownTimeout:   5 * time.Second,
		ReloadInterval:    time.Minute,
	}

	return h
}

// start runs Serve in the background; stop is registered for cleanup.
func (h *harness) start() *harness {
	h.t.Helper()

	srv, err := server.New(h.opts)
	if err != nil {
		h.t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	go func() { h.done <- srv.Serve(ctx) }()

	h.t.Cleanup(func() { h.stop() })

	// Wait until the server has validated the catalog and is accepting, so a
	// test never changes the catalog under its first load.
	if resp := h.get("/healthz"); resp.status != http.StatusOK {
		h.t.Fatalf("GET /healthz after start = %d, want 200", resp.status)
	}

	h.log.reset()

	return h
}

// stop shuts the server down and checks it ended cleanly.
func (h *harness) stop() {
	h.serveOnce.Do(func() {
		h.cancel()

		select {
		case err := <-h.done:
			if err != nil {
				h.t.Errorf("Serve() after shutdown error = %v", err)
			}
		case <-time.After(10 * time.Second):
			h.t.Error("Serve() did not return after its context was canceled")
		}
	})
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (h *harness) do(method, path string, headers map[string]string) response {
	h.t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, "http://screeny.test"+path, http.NoBody)
	if err != nil {
		h.t.Fatalf("building request: %v", err)
	}

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			h.t.Errorf("closing response body: %v", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("reading response body: %v", err)
	}

	return response{status: resp.StatusCode, header: resp.Header, body: body}
}

func (h *harness) get(path string) response { return h.do(http.MethodGet, path, nil) }

// eventually polls until check passes.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if check() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", what)
}

type catalogBody struct {
	GeneratedAt   *string `json:"generated_at"`
	SchemaVersion int     `json:"schema_version"`
	Species       []struct {
		TaxonID int    `json:"taxon_id"`
		Slug    string `json:"slug"`
	} `json:"species"`
}

func (h *harness) catalogTaxa() []int {
	h.t.Helper()

	resp := h.get("/v1/catalog")
	if resp.status != http.StatusOK {
		h.t.Fatalf("GET /v1/catalog = %d", resp.status)
	}

	var body catalogBody
	if err := json.Unmarshal(resp.body, &body); err != nil {
		h.t.Fatalf("decoding catalog: %v", err)
	}

	var ids []int
	for _, s := range body.Species {
		ids = append(ids, s.TaxonID)
	}

	slices.Sort(ids)

	return ids
}

func TestNewRequiresEveryOption(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{})

	tests := map[string]func(o server.Options) server.Options{
		"store":           func(o server.Options) server.Options { o.Store = nil; return o },
		"listener":        func(o server.Options) server.Options { o.Listener = nil; return o },
		"ticker":          func(o server.Options) server.Options { o.Ticker = nil; return o },
		"log":             func(o server.Options) server.Options { o.Log = nil; return o },
		"version":         func(o server.Options) server.Options { o.Version = " "; return o },
		"allowed origins": func(o server.Options) server.Options { o.AllowedOrigins = nil; return o },
		"read header timeout": func(o server.Options) server.Options {
			o.ReadHeaderTimeout = 0
			return o
		},
		"write timeout":    func(o server.Options) server.Options { o.WriteTimeout = -1; return o },
		"shutdown timeout": func(o server.Options) server.Options { o.ShutdownTimeout = 0; return o },
		"reload interval":  func(o server.Options) server.Options { o.ReloadInterval = 0; return o },
	}

	for name, drop := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := server.New(drop(h.opts))
			if !errors.Is(err, server.ErrInvalidServer) || !strings.Contains(err.Error(), name) {
				t.Fatalf("New() error = %v, want ErrInvalidServer naming %q", err, name)
			}
		})
	}

	if _, err := server.New(h.opts); err != nil {
		t.Fatalf("New() with every option error = %v", err)
	}
}

func TestNewReportsEveryProblemTogether(t *testing.T) {
	t.Parallel()

	_, err := server.New(server.Options{})
	if !errors.Is(err, server.ErrInvalidServer) {
		t.Fatalf("New() error = %v, want ErrInvalidServer", err)
	}

	for _, want := range []string{"store", "listener", "ticker", "log", "version", "allowed origins", "read header timeout", "reload interval"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestNewRejectsWildcardAndBlankOrigins(t *testing.T) {
	t.Parallel()

	for _, origin := range []string{"*", "https://*.example.test", "", "  "} {
		h := newHarness(t, []string{origin})

		if _, err := server.New(h.opts); !errors.Is(err, server.ErrInvalidServer) {
			t.Errorf("New() with origin %q error = %v, want ErrInvalidServer", origin, err)
		}
	}
}

func TestServeRefusesToStartOnAnInvalidCatalog(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren)

	// A missing portrait makes the catalog invalid.
	if err := os.Remove(filepath.Join(h.dir, "species", wren, "portrait.png")); err != nil {
		t.Fatalf("removing portrait: %v", err)
	}

	srv, err := server.New(h.opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = srv.Serve(context.Background())
	if !errors.Is(err, catalog.ErrMissingFile) {
		t.Fatalf("Serve() error = %v, want catalog.ErrMissingFile", err)
	}

	if n := h.provider.calls.Load(); n != 0 {
		t.Fatalf("Serve() opened the listener %d times before the catalog validated, want 0", n)
	}
}

func TestServeReportsAListenerFailure(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren)
	cause := errors.New("address in use")
	h.provider.err = cause

	srv, err := server.New(h.opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := srv.Serve(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("Serve() error = %v, want it to wrap %v", err, cause)
	}
}

func TestServeStopsCleanlyWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	if resp := h.get("/healthz"); resp.status != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", resp.status)
	}

	h.stop()

	if !h.provider.listener.closed.Load() {
		t.Error("the listener was left open after shutdown")
	}
}

func TestServeAsksTheTickerForTheConfiguredInterval(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()
	h.get("/healthz")

	if got := time.Duration(h.ticker.every.Load()); got != time.Minute {
		t.Fatalf("ticker interval = %v, want 1m", got)
	}
}

func TestReloadPicksUpNewSpecies(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	if got := h.catalogTaxa(); !slices.Equal(got, []int{1001}) {
		t.Fatalf("taxa = %v, want [1001]", got)
	}

	addSpecies(t, h.dir, finch)
	rebuild(t, h.dir)
	h.ticker.fire()

	eventually(t, "the new species to be served", func() bool {
		return slices.Equal(h.catalogTaxa(), []int{1001, 1002})
	})

	if resp := h.get("/healthz"); resp.status != http.StatusOK {
		t.Fatalf("GET /healthz after a good reload = %d, want 200", resp.status)
	}
}

func TestReloadSkipsAnUnchangedIndex(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()
	h.get("/healthz")

	before := h.store.loads.Load()

	for range 3 {
		h.ticker.fire()
	}

	eventually(t, "the ticks to be checked", func() bool { return h.store.checks.Load() >= 4 })

	if after := h.store.loads.Load(); after != before {
		t.Fatalf("catalog loaded %d times after unchanged ticks, want still %d", after, before)
	}
}

func TestAFailedReloadKeepsTheLastGoodCatalogAndSaysSo(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	// Add a species, then break its portrait before the index is rebuilt over it
	// by deleting the file: the rebuild fails, so write the index by hand.
	addSpecies(t, h.dir, finch)
	rebuild(t, h.dir)

	if err := os.Remove(filepath.Join(h.dir, "species", finch, "portrait.png")); err != nil {
		t.Fatalf("removing portrait: %v", err)
	}

	h.ticker.fire()

	eventually(t, "the failing reload to be reported", func() bool {
		return h.get("/healthz").status == http.StatusServiceUnavailable
	})

	var health map[string]any
	if err := json.Unmarshal(h.get("/healthz").body, &health); err != nil {
		t.Fatalf("decoding health: %v", err)
	}

	if health["ok"] != false || health["reload"] != "failing" {
		t.Errorf("health = %v, want ok=false and reload=failing", health)
	}

	if strings.Contains(string(h.get("/healthz").body), h.dir) {
		t.Error("health leaked the catalog directory path")
	}

	if got := h.catalogTaxa(); !slices.Equal(got, []int{1001}) {
		t.Fatalf("taxa = %v after a failed reload, want the last good [1001]", got)
	}

	// Repairing the catalog recovers on the next change.
	addSpecies(t, h.dir, finch)
	rebuild(t, h.dir)
	h.ticker.fire()

	eventually(t, "recovery", func() bool {
		return h.get("/healthz").status == http.StatusOK && slices.Equal(h.catalogTaxa(), []int{1001, 1002})
	})
}

func TestAFailedReloadIsNotRetriedUntilTheIndexChanges(t *testing.T) {
	t.Parallel()

	h := newHarness(t, []string{}, wren).start()

	addSpecies(t, h.dir, finch)
	rebuild(t, h.dir)

	if err := os.Remove(filepath.Join(h.dir, "species", finch, "portrait.png")); err != nil {
		t.Fatalf("removing portrait: %v", err)
	}

	h.ticker.fire()

	eventually(t, "the failing reload", func() bool { return h.get("/healthz").status == http.StatusServiceUnavailable })

	loads := h.store.loads.Load()

	for range 3 {
		h.ticker.fire()
	}

	eventually(t, "the ticks to be checked", func() bool { return h.store.checks.Load() >= 5 })

	if after := h.store.loads.Load(); after != loads {
		t.Fatalf("a broken catalog was reloaded %d more times without the index changing", after-loads)
	}
}
