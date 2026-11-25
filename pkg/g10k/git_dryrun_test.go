package g10k

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncToModuleDirDryRunDoesNotModifyModule(t *testing.T) {
	rt := newGitTestRuntime(t)
	mirror, _ := newGitModuleMirror(t, rt)
	targetDir := filepath.Join(t.TempDir(), "modules", "module")
	hashFile := filepath.Join(targetDir, ".latest_commit")

	require.True(t, rt.syncToModuleDir(GitModule{git: "module", tree: "1.0.0"}, mirror, targetDir, "env"))
	require.NoError(t, os.WriteFile(hashFile, []byte("outdated"), 0644))

	rt.DryRun = true
	require.True(t, rt.syncToModuleDir(GitModule{git: "module", tree: "1.0.0"}, mirror, targetDir, "env"))
	require.Equal(t, 2, rt.NeedSyncGitCount, "dry run should still report the module as out of sync")
	require.FileExists(t, filepath.Join(targetDir, "manifests", "init.pp"), "dry run must not purge the module")
	content, err := os.ReadFile(hashFile)
	require.NoError(t, err)
	require.Equal(t, "outdated", string(content), "dry run must not update the module")
}

func TestSyncToModuleDirDryRunDoesNotDeployControlRepo(t *testing.T) {
	rt := newGitTestRuntime(t)
	moduleMirror, _ := newGitModuleMirror(t, rt)
	mirror := filepath.Join(rt.Config.EnvCacheDir, "source.git")
	runGit(t, rt.Config.EnvCacheDir, "clone", "-q", "--mirror", moduleMirror, mirror)
	envDir := filepath.Join(t.TempDir(), "environments", "production")

	rt.DryRun = true
	require.True(t, rt.syncToModuleDir(GitModule{tree: "1.0.0"}, mirror, envDir, "production"))
	require.Equal(t, 1, rt.NeedSyncGitCount, "dry run should report the environment as out of sync")
	require.NoDirExists(t, envDir, "dry run must not deploy the control repository")
}
