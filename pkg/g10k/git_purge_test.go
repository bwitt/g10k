package g10k

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// newControlRepoMirror creates a control repo with a Puppetfile and two data files, mirrors it into the
// environments cache dir and returns the work tree and the mirror path.
func newControlRepoMirror(t *testing.T, rt *Runtime) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "control-repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "data", "nodes"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "Puppetfile"), []byte("moduledir 'modules'\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "data", "common.yaml"), []byte("a: 1\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "data", "nodes", "removed.yaml"), []byte("removed: true\n"), 0644))
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "initial")
	mirror := filepath.Join(rt.Config.EnvCacheDir, "source.git")
	runGit(t, rt.Config.EnvCacheDir, "clone", "-q", "--mirror", repo, mirror)
	return repo, mirror
}

func TestSyncToModuleDirControlRepoPurgeLevels(t *testing.T) {
	for _, purgeLevels := range [][]string{{"deployment", "puppetfile"}, {"deployment", "puppetfile", "environment"}} {
		t.Run(filepath.Join(purgeLevels...), func(t *testing.T) {
			rt := newGitTestRuntime(t)
			rt.Config.PurgeLevels = purgeLevels
			repo, mirror := newControlRepoMirror(t, rt)
			envDir := filepath.Join(t.TempDir(), "main")
			gm := GitModule{tree: "main"}

			require.True(t, rt.syncToModuleDir(gm, mirror, envDir, "main"))
			require.FileExists(t, filepath.Join(envDir, "data", "nodes", "removed.yaml"))
			require.NoError(t, os.WriteFile(filepath.Join(envDir, "unmanaged.txt"), []byte("unmanaged\n"), 0644))
			require.NoError(t, os.MkdirAll(filepath.Join(envDir, "modules", "stdlib"), 0755))

			runGit(t, repo, "rm", "-q", "data/nodes/removed.yaml")
			require.NoError(t, os.WriteFile(filepath.Join(repo, "data", "common.yaml"), []byte("a: 2\n"), 0644))
			runGit(t, repo, "commit", "-q", "-am", "change")
			runGit(t, mirror, "remote", "update", "--prune")

			require.True(t, rt.syncToModuleDir(gm, mirror, envDir, "main"))
			content, err := os.ReadFile(filepath.Join(envDir, "data", "common.yaml"))
			require.NoError(t, err)
			require.Equal(t, "a: 2\n", string(content))
			require.NoFileExists(t, filepath.Join(envDir, "data", "nodes", "removed.yaml"), "files removed from the control repo must be removed")
			require.NoDirExists(t, filepath.Join(envDir, "data", "nodes"), "directories that became empty must be removed")
			require.DirExists(t, filepath.Join(envDir, "modules", "stdlib"), "the module dir must be kept")
			if stringSliceContains(purgeLevels, "environment") {
				require.NoFileExists(t, filepath.Join(envDir, "unmanaged.txt"), "unmanaged content must be purged with purge level environment")
			} else {
				require.FileExists(t, filepath.Join(envDir, "unmanaged.txt"), "unmanaged content must be kept without purge level environment")
			}
		})
	}
}
