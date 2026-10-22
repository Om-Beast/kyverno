package source

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5"
	git "github.com/go-git/go-git/v5"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

func TestResolveGitSource(t *testing.T) {
	tests := []struct {
		name, source, branch, realRepo, wantDir, wantBranch string
		attempts []string
	}{
		{
			name: "nested GitLab repository", source: "https://gitlab.example.com/group/subgroup/team/project.git",
			branch: "main", realRepo: "https://gitlab.example.com/group/subgroup/team/project.git",
			wantDir: "/", wantBranch: "main",
			attempts: []string{"https://gitlab.example.com/group/subgroup", "https://gitlab.example.com/group/subgroup/team", "https://gitlab.example.com/group/subgroup/team/project.git"},
		},
		{
			name: "nested repository with policy directory", source: "https://gitlab.example.com/group/subgroup/team/project.git/policies/",
			branch: "main", realRepo: "https://gitlab.example.com/group/subgroup/team/project.git",
			wantDir: "/policies", wantBranch: "main",
			attempts: []string{"https://gitlab.example.com/group/subgroup", "https://gitlab.example.com/group/subgroup/team", "https://gitlab.example.com/group/subgroup/team/project.git"},
		},
		{
			name: "nested repository without git extension", source: "https://gitlab.example.com/group/subgroup/team/project/policies",
			branch: "main", realRepo: "https://gitlab.example.com/group/subgroup/team/project",
			wantDir: "/policies", wantBranch: "main",
			attempts: []string{"https://gitlab.example.com/group/subgroup", "https://gitlab.example.com/group/subgroup/team", "https://gitlab.example.com/group/subgroup/team/project"},
		},
		{
			name: "GitHub branch ending in git", source: "https://github.com/owner/repo/release.git",
			realRepo: "https://github.com/owner/repo", wantDir: "/", wantBranch: "release.git",
			attempts: []string{"https://github.com/owner/repo"},
		},
		{
			name: "flat GitHub with directory", source: "https://github.com/owner/repo/policies",
			branch: "main", realRepo: "https://github.com/owner/repo", wantDir: "/policies", wantBranch: "main",
			attempts: []string{"https://github.com/owner/repo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts []string
			cloner := func(repo string, _ billy.Filesystem, branch string, _ githttp.BasicAuth) (*git.Repository, error) {
				attempts = append(attempts, repo)
				if repo != tt.realRepo {
					return nil, errors.New("repository not found")
				}
				if branch != tt.wantBranch {
					t.Errorf("branch = %q; want %q", branch, tt.wantBranch)
				}
				return nil, nil
			}
			fs, dir, branch, err := ResolveGitSource(tt.source, tt.branch, cloner, githttp.BasicAuth{})
			if err != nil || fs == nil {
				t.Fatalf("ResolveGitSource() fs=%v, error=%v", fs, err)
			}
			if dir != tt.wantDir || branch != tt.wantBranch {
				t.Errorf("directory=%q, branch=%q; want %q, %q", dir, branch, tt.wantDir, tt.wantBranch)
			}
			if !reflect.DeepEqual(attempts, tt.attempts) {
				t.Errorf("clone attempts=%v; want %v", attempts, tt.attempts)
			}
		})
	}
}

func TestResolveGitSourceInvalidInputs(t *testing.T) {
	for _, path := range []string{
		"https://gitlab.example.com",
		"https://gitlab.example.com/",
		"https://gitlab.example.com/group",
		"https://gitlab.example.com/group//repo",
		"https://gitlab.example.com/group/../repo",
		"https://user:secret@gitlab.example.com/group/repo",
		"https://gitlab.example.com/group/repo?token=secret",
		"://bad-url",
	} {
		t.Run(path, func(t *testing.T) {
			cloned := false
			cloner := func(string, billy.Filesystem, string, githttp.BasicAuth) (*git.Repository, error) {
				cloned = true
				return nil, nil
			}
			_, _, _, err := ResolveGitSource(path, "main", cloner, githttp.BasicAuth{})
			if err == nil || cloned {
				t.Fatalf("invalid source %q: error=%v, cloned=%v", path, err, cloned)
			}
		})
	}
}

func TestResolveGitSourceCloneFailure(t *testing.T) {
	cloner := func(string, billy.Filesystem, string, githttp.BasicAuth) (*git.Repository, error) {
		return nil, errors.New("repository not found")
	}
	_, _, _, err := ResolveGitSource("https://gitlab.example.com/group/subgroup/project", "main", cloner, githttp.BasicAuth{})
	if err == nil || !strings.Contains(err.Error(), "repository not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}
