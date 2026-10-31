// Package upstream reads a catalog from a GitHub repository: it resolves a ref
// to one commit and fetches raw files at that commit.
package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strings"
)

const (
	apiBase   = "https://api.github.com"
	rawBase   = "https://raw.githubusercontent.com"
	userAgent = "screeny-bird"

	// maxBodyBytes bounds every response, because upstream content is untrusted.
	// It is a safety invariant, not an operator setting.
	maxBodyBytes = 32 << 20
)

var (
	// ErrInvalidSource reports an unusable repository, ref, client, commit or path.
	ErrInvalidSource = errors.New("invalid upstream source")
	// ErrNotFound reports a ref or file that does not exist upstream.
	ErrNotFound = errors.New("not found upstream")
	// ErrBadResponse reports an upstream response the service cannot trust or use.
	ErrBadResponse = errors.New("unexpected upstream response")
)

var (
	repoPattern    = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	refPattern     = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	segmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// doer is the one thing a Source needs from an HTTP client.
type doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Source reads one GitHub repository at one ref.
type Source struct {
	repo   string
	ref    string
	client doer
}

// NewSource returns a source for the repository repo ("owner/name") at ref,
// using client for every request. It reports every invalid argument together.
func NewSource(repo, ref string, client doer) (Source, error) {
	var problems []error

	if !repoPattern.MatchString(repo) || strings.Contains(repo, "..") {
		problems = append(problems, fmt.Errorf("repo %q must look like owner/name", repo))
	}

	if !refPattern.MatchString(ref) || strings.Contains(ref, "..") {
		problems = append(problems, fmt.Errorf("ref %q must be a branch, tag or similar name", ref))
	}

	if client == nil {
		problems = append(problems, errors.New("client is required"))
	}

	if err := errors.Join(problems...); err != nil {
		return Source{}, fmt.Errorf("creating upstream source: %w: %w", ErrInvalidSource, err)
	}

	return Source{repo: repo, ref: ref, client: client}, nil
}

// ResolveRef returns the commit SHA the source's ref points to right now.
func (s Source) ResolveRef(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/commits/%s", apiBase, s.repo, s.ref)

	body, err := s.get(ctx, url, map[string]string{
		"Accept":               "application/vnd.github.sha",
		"X-GitHub-Api-Version": "2022-11-28",
	})
	if err != nil {
		return "", fmt.Errorf("resolving %s at %s: %w", s.ref, s.repo, err)
	}

	sha := strings.TrimSpace(string(body))
	if !commitPattern.MatchString(sha) {
		return "", fmt.Errorf("resolving %s at %s: response is not a commit SHA: %w", s.ref, s.repo, ErrBadResponse)
	}

	return sha, nil
}

// Fetch returns the raw content of the file at filePath in the repository at
// the given commit. commit must be a full SHA and filePath a clean relative
// path, so a ref can never change what one run reads.
func (s Source) Fetch(ctx context.Context, commit, filePath string) ([]byte, error) {
	if !commitPattern.MatchString(commit) {
		return nil, fmt.Errorf("fetching %q: commit %q is not a full SHA: %w", filePath, commit, ErrInvalidSource)
	}

	if err := checkPath(filePath); err != nil {
		return nil, fmt.Errorf("fetching %q: %w", filePath, err)
	}

	url := fmt.Sprintf("%s/%s/%s/%s", rawBase, s.repo, commit, filePath)

	body, err := s.get(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching %s at %s: %w", filePath, commit, err)
	}

	return body, nil
}

func checkPath(filePath string) error {
	if filePath == "" || path.IsAbs(filePath) || path.Clean(filePath) != filePath {
		return fmt.Errorf("path must be a clean relative path: %w", ErrInvalidSource)
	}

	for segment := range strings.SplitSeq(filePath, "/") {
		if segment == ".." || !segmentPattern.MatchString(segment) {
			return fmt.Errorf("path segment %q is not allowed: %w", segment, ErrInvalidSource)
		}
	}

	return nil
}

func (s Source) get(ctx context.Context, url string, headers map[string]string) (body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", url, err)
	}

	req.Header.Set("User-Agent", userAgent)

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", url, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing response from %s: %w", url, closeErr))
		}
	}()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("%s: %w", url, ErrNotFound)
	default:
		return nil, fmt.Errorf("%s answered %d: %w", url, resp.StatusCode, ErrBadResponse)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", url, err)
	}

	if len(data) > maxBodyBytes {
		return nil, fmt.Errorf("%s is over the %d byte limit: %w", url, maxBodyBytes, ErrBadResponse)
	}

	return data, nil
}
