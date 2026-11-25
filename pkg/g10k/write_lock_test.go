package g10k

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunRefusesToDeployWithWriteLock(t *testing.T) {
	dir := t.TempDir()
	basedir := filepath.Join(dir, "environments")
	config := filepath.Join(dir, "g10k.yaml")
	require.NoError(t, os.WriteFile(config, []byte(`---
cachedir: '`+filepath.Join(dir, "cache")+`'
deploy:
  write_lock: 'Deploys are frozen during the maintenance window'
sources:
  example:
    remote: '`+filepath.Join(dir, "missing.git")+`'
    basedir: '`+basedir+`'
`), 0644))

	code, err := Run(Options{ConfigFile: config, Quiet: true, MaxWorker: 1, MaxExtractWorker: 1})
	require.Equal(t, 16, code)
	require.ErrorContains(t, err, "Deploys are frozen during the maintenance window")
	require.NoDirExists(t, basedir, "nothing should be deployed while write_lock is set")
}
