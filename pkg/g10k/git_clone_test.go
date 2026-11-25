package g10k

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncToModuleDirCloneGitModulesIsNotResyncedWhenUnchanged(t *testing.T) {
	rt := newGitTestRuntime(t)
	rt.Config.CloneGitModules = true
	mirror, commit := newGitModuleMirror(t, rt)
	targetDir := filepath.Join(t.TempDir(), "modules", "module")
	gm := GitModule{git: mirror, tree: "1.0.1"}

	require.True(t, rt.syncToModuleDir(gm, mirror, targetDir, "env"))
	require.Equal(t, 1, rt.NeedSyncGitCount)
	require.DirExists(t, filepath.Join(targetDir, ".git"), "module should be a git clone")
	latestCommit, err := os.ReadFile(filepath.Join(targetDir, ".latest_commit"))
	require.NoError(t, err)
	require.Equal(t, commit, string(latestCommit))

	require.True(t, rt.syncToModuleDir(gm, mirror, targetDir, "env"))
	require.Equal(t, 1, rt.NeedSyncGitCount, "unchanged module should not be cloned again")
}
