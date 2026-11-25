package g10k

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncToModuleDirTagIsNotResyncedWhenUnchanged(t *testing.T) {
	for _, tag := range []string{"1.0.0", "1.0.1"} {
		t.Run(tag, func(t *testing.T) {
			rt := newGitTestRuntime(t)
			mirror, commit := newGitModuleMirror(t, rt)
			targetDir := filepath.Join(t.TempDir(), "modules", "module")
			gm := GitModule{git: "module", tree: tag}

			require.True(t, rt.syncToModuleDir(gm, mirror, targetDir, "env"))
			require.Equal(t, 1, rt.NeedSyncGitCount)
			latestCommit, err := os.ReadFile(filepath.Join(targetDir, ".latest_commit"))
			require.NoError(t, err)
			require.Equal(t, commit, string(latestCommit), ".latest_commit should contain the commit the tag points to")

			require.True(t, rt.syncToModuleDir(gm, mirror, targetDir, "env"))
			require.Equal(t, 1, rt.NeedSyncGitCount, "unchanged tag should not be synced again")
		})
	}
}
