package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

const (
	immutableCache = "public, max-age=31536000, immutable"
	revalidate     = "no-cache"
	noStore        = "no-store"
)

// routes builds the handler tree. Every route answers only GET and HEAD, every
// response carries nosniff, and every finished request is logged.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("/healthz", methodNotAllowed)
	mux.HandleFunc("GET /v1/catalog", s.withCORS(s.catalog))
	mux.HandleFunc("/v1/catalog", methodNotAllowed)
	mux.HandleFunc("GET /v1/assets/{path...}", s.withCORS(s.asset))
	mux.HandleFunc("/v1/assets/{path...}", methodNotAllowed)
	mux.HandleFunc("/", notFound)

	return s.logged(nosniff(mux))
}

func nosniff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// recorder remembers the status and size of a response for the access log.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}

	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	n, err := r.ResponseWriter.Write(data)
	r.bytes += n

	// A ResponseWriter's error passes through unchanged: the net/http server
	// interprets it.
	return n, err
}

func (s *Server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}

		// A HEAD response carries no body, whatever the handler wrote.
		sent := rec.bytes
		if r.Method == http.MethodHead {
			sent = 0
		}

		// The escaped path keeps control characters out of the log, and the
		// query string is not logged at all.
		s.opts.Log.Log(r.Method, r.URL.EscapedPath(), status, sent)
	})
}

// withCORS lets an exactly listed origin read the response. The response always
// varies on Origin, a wildcard is never sent, and credentials are never allowed.
func (s *Server) withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")

		if origin := r.Header.Get("Origin"); origin != "" && s.allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}

		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"ok":false,"error":"internal error"}`)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", noStore)
	w.WriteHeader(status)

	// The client may have gone away; there is nobody left to tell.
	_, _ = w.Write(data)
}

type errorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func notFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
}

func methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
}

type healthResponse struct {
	OK          bool   `json:"ok"`
	Version     string `json:"version"`
	Species     int    `json:"species"`
	GeneratedAt string `json:"generated_at,omitempty"`
	// Reload is "failing" while the latest reload failed and an older, valid
	// catalog is being served. Details are left out: they would reveal paths.
	Reload string `json:"reload,omitempty"`
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	snap := s.state.Load()
	body := healthResponse{OK: true, Version: s.opts.Version, Species: snap.species, GeneratedAt: snap.generatedAt}
	status := http.StatusOK

	if s.reloadFailing.Load() {
		body.OK = false
		body.Reload = "failing"
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, body)
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	snap := s.state.Load()

	w.Header().Set("ETag", snap.catalogETag)
	w.Header().Set("Cache-Control", revalidate)

	if etagMatches(r.Header.Get("If-None-Match"), snap.catalogETag) {
		w.WriteHeader(http.StatusNotModified)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(snap.catalogJSON)))

	// The client may have gone away; there is nobody left to tell.
	_, _ = w.Write(snap.catalogJSON)
}

// asset serves one image the catalog lists, after checking its bytes against
// the SHA-256 the catalog declares. A file that fails the check is never sent.
func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	snap := s.state.Load()
	name := r.PathValue("path")

	want, listed := snap.assets[name]
	if !listed {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})

		return
	}

	etag := `"` + want + `"`
	cache := revalidate

	// A request that names the exact digest can be cached forever, because the
	// content at that address can never change.
	if digests := r.URL.Query()["sha256"]; len(digests) == 1 && digests[0] == want {
		cache = immutableCache
	}

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", cache)

	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)

		return
	}

	data, err := s.opts.Store.ReadAsset(name)
	if err != nil {
		unavailable(w)

		return
	}

	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != want {
		unavailable(w)

		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))

	// The client may have gone away; there is nobody left to tell.
	_, _ = w.Write(data)
}

func unavailable(w http.ResponseWriter) {
	for _, header := range []string{"ETag", "Cache-Control"} {
		w.Header().Del(header)
	}

	writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "asset unavailable"})
}

// etagMatches reports whether an If-None-Match header names etag, ignoring a
// weak prefix and honouring the * wildcard and comma-separated lists.
func etagMatches(header, etag string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")

		if candidate == "*" || candidate == etag {
			return true
		}
	}

	return false
}
