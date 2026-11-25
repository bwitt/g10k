package g10k

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeValidateConfig(t *testing.T, dir string, source string) string {
	t.Helper()
	config := filepath.Join(dir, "g10k.yaml")
	require.NoError(t, os.WriteFile(config, []byte(`---
cachedir: '`+filepath.Join(dir, "cache")+`'
sources:
  example:
`+source+`
    basedir: '`+filepath.Join(dir, "environments")+`'
`), 0644))
	return config
}

func TestRunValidateDoesNotDeploy(t *testing.T) {
	dir := t.TempDir()
	config := writeValidateConfig(t, dir, "    remote: '"+filepath.Join(dir, "control-repo.git")+"'")

	code, err := Run(Options{ConfigFile: config, Validate: true, MaxWorker: 1, MaxExtractWorker: 1})
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.NoDirExists(t, filepath.Join(dir, "environments"), "-validate must not deploy anything")
}

func TestRunValidateChecksSources(t *testing.T) {
	dir := t.TempDir()
	config := writeValidateConfig(t, dir, "    private_key: '"+filepath.Join(dir, "missing_key")+"'")

	code, err := Run(Options{ConfigFile: config, Validate: true, MaxWorker: 1, MaxExtractWorker: 1})
	require.ErrorIs(t, err, ErrValidationFailed)
	require.Equal(t, 1, code)
	require.NoDirExists(t, filepath.Join(dir, "environments"), "-validate must not deploy anything")
}

func TestRunValidatePuppetfileDoesNotDeploy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("g10k_cachedir", filepath.Join(dir, "cache"))
	module := filepath.Join(dir, "module")
	require.NoError(t, os.Mkdir(module, 0755))
	runGit(t, module, "init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(module, "README.md"), []byte("module\n"), 0644))
	runGit(t, module, "add", "-A")
	runGit(t, module, "commit", "-q", "-m", "initial")
	puppetfile := filepath.Join(dir, "Puppetfile")
	require.NoError(t, os.WriteFile(puppetfile, []byte("mod 'module',\n  :git => '"+module+"'\n"), 0644))
	moduleDir := filepath.Join(dir, "modules")

	code, err := Run(Options{PFMode: true, PFLocation: puppetfile, ModuleDir: moduleDir, Validate: true, MaxWorker: 1, MaxExtractWorker: 1})
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.NoDirExists(t, moduleDir, "-validate must not deploy anything")
}
