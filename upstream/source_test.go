package upstream_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rusher2004/screeny-bird/upstream"
)

const commit = "8bc988b239d7ffb7d7394b1e5ad174b90f18ae1e"

type trackedBody struct {
	io.Reader
	closed *bool
}

func (b trackedBody) Close() error {
	*b.closed = true

	return nil
}

type erroringReader struct{ err error }

func (r erroringReader) Read([]byte) (int, error) { return 0, r.err }

type fakeDoer struct {
	requests []*http.Request
	status   int
	body     []byte
	err      error
	// readErr, if set, makes reading the response body fail after body.
	readErr error
	closed  bool
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, req)

	if f.err != nil {
		return nil, f.err
	}

	var reader io.Reader = bytes.NewReader(f.body)
	if f.readErr != nil {
		reader = io.MultiReader(reader, erroringReader{err: f.readErr})
	}

	return &http.Response{
		StatusCode: f.status,
		Body:       trackedBody{Reader: reader, closed: &f.closed},
	}, nil
}

func newSource(t *testing.T, doer *fakeDoer) upstream.Source {
	t.Helper()

	source, err := upstream.NewSource("veteranbv/inky-bird-frame", "main", doer)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}

	return source
}

func TestNewSourceRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	doer := &fakeDoer{}

	tests := map[string]struct {
		repo, ref string
		client    *fakeDoer
	}{
		"empty repo":         {repo: "", ref: "main", client: doer},
		"repo without owner": {repo: "inky-bird-frame", ref: "main", client: doer},
		"repo with a scheme": {repo: "https://github.com/a/b", ref: "main", client: doer},
		"repo with dots":     {repo: "../etc/passwd", ref: "main", client: doer},
		"empty ref":          {repo: "a/b", ref: "", client: doer},
		"ref with dotdot":    {repo: "a/b", ref: "feature/../main", client: doer},
		"ref with a space":   {repo: "a/b", ref: "my branch", client: doer},
		"ref with a query":   {repo: "a/b", ref: "main?x=1", client: doer},
		"ref with a slash":   {repo: "a/b", ref: "/main", client: doer},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := upstream.NewSource(tt.repo, tt.ref, tt.client); !errors.Is(err, upstream.ErrInvalidSource) {
				t.Fatalf("NewSource(%q, %q) error = %v, want ErrInvalidSource", tt.repo, tt.ref, err)
			}
		})
	}

	if _, err := upstream.NewSource("a/b", "main", nil); !errors.Is(err, upstream.ErrInvalidSource) {
		t.Fatalf("NewSource() without a client error = %v, want ErrInvalidSource", err)
	}

	if _, err := upstream.NewSource("a/b", "release/v1.2", doer); err != nil {
		t.Fatalf("NewSource() with a slashed ref error = %v", err)
	}
}

func TestNewSourceReportsEveryProblem(t *testing.T) {
	t.Parallel()

	_, err := upstream.NewSource("", "", nil)
	if !errors.Is(err, upstream.ErrInvalidSource) {
		t.Fatalf("NewSource() error = %v, want ErrInvalidSource", err)
	}

	for _, want := range []string{"repo", "ref", "client"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestResolveRefAsksTheGitHubAPIForTheCommit(t *testing.T) {
	t.Parallel()

	doer := &fakeDoer{status: http.StatusOK, body: []byte(commit + "\n")}

	got, err := newSource(t, doer).ResolveRef(context.Background())
	if err != nil || got != commit {
		t.Fatalf("ResolveRef() = %q, %v; want %q", got, err, commit)
	}

	if len(doer.requests) != 1 {
		t.Fatalf("made %d requests, want 1", len(doer.requests))
	}

	req := doer.requests[0]
	if req.Method != http.MethodGet || req.URL.String() != "https://api.github.com/repos/veteranbv/inky-bird-frame/commits/main" {
		t.Errorf("request = %s %s", req.Method, req.URL)
	}

	if req.Header.Get("Accept") != "application/vnd.github.sha" || req.Header.Get("User-Agent") == "" {
		t.Errorf("headers = %v, want the sha media type and a user agent", req.Header)
	}

	if !doer.closed {
		t.Error("response body was not closed")
	}
}

func TestResolveRefRejectsBadResponses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		doer *fakeDoer
		want error
	}{
		"not found":          {doer: &fakeDoer{status: http.StatusNotFound}, want: upstream.ErrNotFound},
		"server error":       {doer: &fakeDoer{status: http.StatusInternalServerError}, want: upstream.ErrBadResponse},
		"rate limited":       {doer: &fakeDoer{status: http.StatusForbidden}, want: upstream.ErrBadResponse},
		"redirect":           {doer: &fakeDoer{status: http.StatusFound}, want: upstream.ErrBadResponse},
		"not a commit":       {doer: &fakeDoer{status: http.StatusOK, body: []byte("not-a-sha")}, want: upstream.ErrBadResponse},
		"short commit":       {doer: &fakeDoer{status: http.StatusOK, body: []byte("8bc988b")}, want: upstream.ErrBadResponse},
		"uppercase commit":   {doer: &fakeDoer{status: http.StatusOK, body: []byte(strings.ToUpper(commit))}, want: upstream.ErrBadResponse},
		"transport failure":  {doer: &fakeDoer{err: errors.New("connection reset")}, want: nil},
		"empty body":         {doer: &fakeDoer{status: http.StatusOK}, want: upstream.ErrBadResponse},
		"commit then extras": {doer: &fakeDoer{status: http.StatusOK, body: []byte(commit + " extra")}, want: upstream.ErrBadResponse},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := newSource(t, tt.doer).ResolveRef(context.Background())
			if err == nil {
				t.Fatal("ResolveRef() error = nil, want failure")
			}

			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("ResolveRef() error = %v, want %v", err, tt.want)
			}

			if tt.doer.err != nil && !errors.Is(err, tt.doer.err) {
				t.Fatalf("ResolveRef() error = %v, want it to wrap the transport error", err)
			}
		})
	}
}

func TestFetchReadsARawFileAtACommit(t *testing.T) {
	t.Parallel()

	doer := &fakeDoer{status: http.StatusOK, body: []byte("hello")}

	got, err := newSource(t, doer).Fetch(context.Background(), commit, "catalog/species/1-a/manifest.json")
	if err != nil || string(got) != "hello" {
		t.Fatalf("Fetch() = %q, %v; want hello", got, err)
	}

	want := "https://raw.githubusercontent.com/veteranbv/inky-bird-frame/" + commit + "/catalog/species/1-a/manifest.json"
	if len(doer.requests) != 1 || doer.requests[0].URL.String() != want {
		t.Fatalf("requests = %v, want one for %s", doer.requests, want)
	}

	if !doer.closed {
		t.Error("response body was not closed")
	}
}

func TestFetchMapsStatusesToErrors(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status int
		want   error
	}{
		"not found":    {status: http.StatusNotFound, want: upstream.ErrNotFound},
		"server error": {status: http.StatusBadGateway, want: upstream.ErrBadResponse},
		"redirect":     {status: http.StatusMovedPermanently, want: upstream.ErrBadResponse},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := newSource(t, &fakeDoer{status: tt.status}).Fetch(context.Background(), commit, "catalog/index.json")
			if !errors.Is(err, tt.want) {
				t.Fatalf("Fetch() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestFetchBoundsTheBody(t *testing.T) {
	t.Parallel()

	// The documented per-file limit is 32 MiB.
	const limit = 32 << 20

	atLimit := &fakeDoer{status: http.StatusOK, body: bytes.Repeat([]byte{1}, limit)}
	if _, err := newSource(t, atLimit).Fetch(context.Background(), commit, "catalog/a.png"); err != nil {
		t.Fatalf("Fetch() of exactly the limit error = %v", err)
	}

	overLimit := &fakeDoer{status: http.StatusOK, body: bytes.Repeat([]byte{1}, limit+1)}
	if _, err := newSource(t, overLimit).Fetch(context.Background(), commit, "catalog/a.png"); !errors.Is(err, upstream.ErrBadResponse) {
		t.Fatalf("Fetch() over the limit error = %v, want ErrBadResponse", err)
	}
}

func TestFetchRejectsUnsafeArgumentsWithoutRequesting(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ commit, path string }{
		"branch name as commit": {commit: "main", path: "catalog/index.json"},
		"short commit":          {commit: "8bc988b", path: "catalog/index.json"},
		"empty path":            {commit: commit, path: ""},
		"absolute path":         {commit: commit, path: "/catalog/index.json"},
		"parent traversal":      {commit: commit, path: "catalog/../../etc/passwd"},
		"unclean path":          {commit: commit, path: "catalog//index.json"},
		"query in path":         {commit: commit, path: "catalog/index.json?raw=1"},
		"space in path":         {commit: commit, path: "catalog/my file.json"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doer := &fakeDoer{status: http.StatusOK}

			if _, err := newSource(t, doer).Fetch(context.Background(), tt.commit, tt.path); !errors.Is(err, upstream.ErrInvalidSource) {
				t.Fatalf("Fetch(%q, %q) error = %v, want ErrInvalidSource", tt.commit, tt.path, err)
			}

			if len(doer.requests) != 0 {
				t.Fatalf("made %d requests for an unsafe argument", len(doer.requests))
			}
		})
	}
}

func TestFetchWrapsTransportFailuresAndHonoursContext(t *testing.T) {
	t.Parallel()

	cause := errors.New("connection reset")

	if _, err := newSource(t, &fakeDoer{err: cause}).Fetch(context.Background(), commit, "catalog/index.json"); !errors.Is(err, cause) {
		t.Fatalf("Fetch() error = %v, want it to wrap %v", err, cause)
	}

	type key struct{}

	ctx := context.WithValue(context.Background(), key{}, "marker")
	doer := &fakeDoer{status: http.StatusOK, body: []byte("x")}

	if _, err := newSource(t, doer).Fetch(ctx, commit, "catalog/index.json"); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if doer.requests[0].Context().Value(key{}) != "marker" {
		t.Error("request did not carry the caller's context")
	}
}

func TestFailuresAreMarkedTransientOnlyWhenARetryCouldHelp(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		doer      *fakeDoer
		transient bool
	}{
		"too many requests":   {doer: &fakeDoer{status: http.StatusTooManyRequests}, transient: true},
		"internal error":      {doer: &fakeDoer{status: http.StatusInternalServerError}, transient: true},
		"bad gateway":         {doer: &fakeDoer{status: http.StatusBadGateway}, transient: true},
		"service unavailable": {doer: &fakeDoer{status: http.StatusServiceUnavailable}, transient: true},
		"gateway timeout":     {doer: &fakeDoer{status: http.StatusGatewayTimeout}, transient: true},
		"connection failure":  {doer: &fakeDoer{err: errors.New("connection reset")}, transient: true},
		"body read failure":   {doer: &fakeDoer{status: http.StatusOK, body: []byte("par"), readErr: errors.New("stalled")}, transient: true},
		"not found":           {doer: &fakeDoer{status: http.StatusNotFound}, transient: false},
		"bad request":         {doer: &fakeDoer{status: http.StatusBadRequest}, transient: false},
		"forbidden":           {doer: &fakeDoer{status: http.StatusForbidden}, transient: false},
		"redirect":            {doer: &fakeDoer{status: http.StatusFound}, transient: false},
		"oversize body":       {doer: &fakeDoer{status: http.StatusOK, body: bytes.Repeat([]byte{1}, 32<<20+1)}, transient: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, fetchErr := newSource(t, tt.doer).Fetch(context.Background(), commit, "catalog/index.json")
			if fetchErr == nil {
				t.Fatal("Fetch() error = nil, want failure")
			}

			if got := errors.Is(fetchErr, upstream.ErrTransient); got != tt.transient {
				t.Errorf("Fetch() error %v: transient = %t, want %t", fetchErr, got, tt.transient)
			}

			_, resolveErr := newSource(t, tt.doer).ResolveRef(context.Background())
			if got := errors.Is(resolveErr, upstream.ErrTransient); got != tt.transient {
				t.Errorf("ResolveRef() error %v: transient = %t, want %t", resolveErr, got, tt.transient)
			}
		})
	}
}

func TestACanceledRequestIsNeverTransient(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for name, doer := range map[string]*fakeDoer{
		"transport failure": {err: context.Canceled},
		"body read failure": {status: http.StatusOK, readErr: context.Canceled},
	} {
		_, err := newSource(t, doer).Fetch(ctx, commit, "catalog/index.json")
		if err == nil || errors.Is(err, upstream.ErrTransient) {
			t.Errorf("%s: Fetch() on a canceled context error = %v, want a non-transient failure", name, err)
		}
	}
}
