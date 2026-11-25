package g10k

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// runGit runs a git command in dir and returns its trimmed output.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=g10k", "GIT_AUTHOR_EMAIL=g10k@example.com",
		"GIT_COMMITTER_NAME=g10k", "GIT_COMMITTER_EMAIL=g10k@example.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// newGitModuleMirror creates a module repository with an annotated and a
// lightweight tag, mirrors it into the modules cache dir and returns the
// mirror path and the tagged commit.
func newGitModuleMirror(t *testing.T, rt *Runtime) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "module")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "manifests"), 0755))
	runGit(t, repo, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "manifests", "init.pp"), []byte("class module {}\n"), 0644))
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "initial")
	runGit(t, repo, "tag", "-a", "1.0.0", "-m", "annotated")
	runGit(t, repo, "tag", "1.0.1")
	commit := runGit(t, repo, "rev-parse", "HEAD")

	mirror := filepath.Join(rt.Config.ModulesCacheDir, "module.git")
	runGit(t, rt.Config.ModulesCacheDir, "clone", "-q", "--mirror", repo, mirror)
	return mirror, commit
}

func newGitTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	cacheDir := t.TempDir()
	rt := NewRuntime(Options{Quiet: true})
	rt.Config = ConfigSettings{
		CacheDir:        cacheDir,
		ModulesCacheDir: filepath.Join(cacheDir, "modules"),
		EnvCacheDir:     filepath.Join(cacheDir, "environments"),
		Timeout:         30,
	}
	require.NoError(t, os.MkdirAll(rt.Config.ModulesCacheDir, 0755))
	require.NoError(t, os.MkdirAll(rt.Config.EnvCacheDir, 0755))
	return rt
}
