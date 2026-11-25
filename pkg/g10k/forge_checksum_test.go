package g10k

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInvalidSha256sumForgemoduleIsNotCached verifies that a Forge module which
// failed the integrity check is neither kept in the cache nor deployed on the next run.
func TestInvalidSha256sumForgemoduleIsNotCached(t *testing.T) {
	if dir := os.Getenv("TEST_FOR_CRASH_SHA256_CACHE_DIR"); dir != "" {
		rt := NewRuntime(Options{Quiet: true})
		ts := spinUpFakeForge(t, "../../tests/fake-forge/invalid-sha256sum-puppetlabs-ntp-metadata.json")
		defer ts.Close()
		fm := map[string]ForgeModule{"puppetlabs/ntp": {version: "6.0.0", name: "ntp", author: "puppetlabs",
			baseURL: ts.URL, sha256sum: "a988a172a3edde6ac2a26d0e893faa88d37bc47465afc50d55225a036906c944"}}
		pfm := map[string]Puppetfile{"test": {forgeModules: fm, source: "test", forgeBaseURL: ts.URL,
			workDir: filepath.Join(dir, "env"), moduleDirs: []string{"modules"}}}
		rt.Config = ConfigSettings{ForgeCacheDir: filepath.Join(dir, "forge"), Maxworker: 1, MaxExtractworker: 1}
		rt.checkDirAndCreate(rt.Config.ForgeCacheDir, "forge cache")
		_ = rt.resolvePuppetfile(pfm)
		return
	}

	dir := t.TempDir()
	for run := 1; run <= 2; run++ {
		cmd := exec.Command(os.Args[0], "-test.run=TestInvalidSha256sumForgemoduleIsNotCached$")
		cmd.Env = append(os.Environ(), "TEST_FOR_CRASH_SHA256_CACHE_DIR="+dir)
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr, "run %d should fail because of the sha256sum mismatch: %s", run, out)
		require.Contains(t, string(out), "does not match expected sha256sum", "run %d", run)
		require.NoFileExists(t, filepath.Join(dir, "forge", "puppetlabs-ntp-6.0.0.tar.gz"), "run %d", run)
		require.NoDirExists(t, filepath.Join(dir, "forge", "puppetlabs-ntp-6.0.0"), "run %d", run)
		require.NoDirExists(t, filepath.Join(dir, "env", "modules", "ntp"), "run %d", run)
	}
}
