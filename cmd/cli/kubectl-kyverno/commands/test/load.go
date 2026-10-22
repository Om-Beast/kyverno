package test

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/kyverno/kyverno/cmd/cli/kubectl-kyverno/source"
	"github.com/kyverno/kyverno/cmd/cli/kubectl-kyverno/test"
	gitutils "github.com/kyverno/kyverno/pkg/utils/git"
)

func loadTests(paths []string, fileName string, gitBranch string) (test.TestCases, error) {
	var tests []test.TestCase
	for _, path := range paths {
		t, err := loadTest(path, fileName, gitBranch)
		if err != nil {
			return nil, err
		}
		tests = append(tests, t...)
	}
	return tests, nil
}

func loadTest(path string, fileName string, gitBranch string) (test.TestCases, error) {
	var tests []test.TestCase
	if source.IsGit(path) {
		fs, gitPathToYamls, _, err := source.ResolveGitSource(path, gitBranch, gitutils.Clone, http.BasicAuth{})
		if err != nil {
			return nil, fmt.Errorf("failed to clone Git repository: %w", err)
		}
		yamlFiles, err := gitutils.ListYamls(fs, gitPathToYamls)
		if err != nil {
			return nil, fmt.Errorf("failed to list YAMLs in repository: %w", err)
		}
		sort.Strings(yamlFiles)
		for _, yamlFilePath := range yamlFiles {
			if filepath.Base(yamlFilePath) == fileName {
				tests = append(tests, test.LoadTest(fs, yamlFilePath)...)
			}
		}
		return tests, nil
	} else {
		tests, err := test.LoadTests(path, fileName)
		return tests, err
	}
}
