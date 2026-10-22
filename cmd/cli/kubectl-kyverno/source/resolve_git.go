package source

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitutils "github.com/kyverno/kyverno/pkg/utils/git"
)

// ResolveGitSource locates a Git repository even when its hosting namespace has
// multiple levels, and returns the cloned filesystem and remaining in-repo path.
// The explicitly supplied branch is used for every attempt. Without a branch,
// a trailing path segment is interpreted as a branch, preserving CLI behavior.
func ResolveGitSource(rawURL, branch string, clone gitutils.CloneFunc, auth githttp.BasicAuth) (billy.Filesystem, string, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse Git source URL: %w", err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, "", "", fmt.Errorf("invalid Git source URL: expected an HTTP(S) repository URL without userinfo, query or fragment")
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) < 2 {
		return nil, "", "", fmt.Errorf("invalid Git source URL path: expected at least namespace/repository")
	}
	for _, part := range segments {
		if part == "" || part == "." || part == ".." {
			return nil, "", "", fmt.Errorf("invalid Git source URL path segment")
		}
	}
	if clone == nil {
		return nil, "", "", fmt.Errorf("Git clone function must not be nil")
	}

	// Prefer the conventional owner/repo layout but allow additional GitLab
	// namespace segments. Recreate the filesystem for every attempt to avoid
	// mixing partial state from failed clones.
	var lastErr error
	for n := 2; n <= len(segments); n++ {
		candidate := *u
		candidate.Path = "/" + strings.Join(segments[:n], "/")
		candidate.RawPath = ""
		repoURL := candidate.String()
		remainder := segments[n:]
		selectedBranch := branch
		dir := "/"
		if selectedBranch == "" {
			selectedBranch = "main"
			if len(remainder) > 0 {
				selectedBranch = remainder[0]
			}
		} else if len(remainder) > 0 {
			dir += strings.Join(remainder, "/")
		}
		fs := memfs.New()
		if _, err := clone(repoURL, fs, selectedBranch, auth); err != nil {
			lastErr = fmt.Errorf("clone %q: %w", repoURL, err)
			continue
		}
		return fs, dir, selectedBranch, nil
	}
	return nil, "", "", fmt.Errorf("could not resolve Git repository from %q: %w", rawURL, lastErr)
}
