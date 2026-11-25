//go:build unix

package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const emptyPuppetfile = "moduledir 'modules'\n"

func TestVersionAndHelp(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)

	res := e.mustRun("-version")
	assert.Contains(t, res.stdout, "g10k")

	res = e.mustRun("-help")
	assert.Contains(t, res.stderr, "-config")

	res = e.run()
	assert.Equal(t, 1, res.code, res.output())
	assert.Contains(t, res.stderr, "you need to specify at least a config file or use the Puppetfile mode")
}

// newControlRepo creates a control repo with the branches main, production
// and feature/foo-bar and returns it with the commit of each branch.
func newControlRepo(e *testEnv) (*gitRepo, map[string]string) {
	cr := e.newRepo("control")
	cr.write("Puppetfile", emptyPuppetfile)
	cr.write("manifests/site.pp", "node default {}\n")
	commits := map[string]string{"main": cr.commit("main")}
	cr.branch("production", "main")
	cr.write("data/production.yaml", "env: production\n")
	commits["production"] = cr.commit("production")
	cr.branch("feature/foo-bar", "main")
	cr.write("data/feature.yaml", "env: feature\n")
	commits["feature/foo-bar"] = cr.commit("feature")
	cr.checkout("main")
	return cr, commits
}

func TestDeployAllBranches(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr, commits := newControlRepo(e)
	cfg := e.config(cr.dir, unreachableForge, "")

	res := e.mustRun("-config", cfg, "-info")
	assert.Contains(t, res.stdout, "Renaming branch feature/foo-bar to feature_foo_bar")
	assert.ElementsMatch(t, []string{"example_main", "example_production", "example_feature_foo_bar"}, dirNames(t, e.basedir))

	envs := map[string]string{
		"example_main":            "main",
		"example_production":      "production",
		"example_feature_foo_bar": "feature/foo-bar",
	}
	for env, branch := range envs {
		envDir := e.env(env)
		assert.FileExists(t, filepath.Join(envDir, "manifests", "site.pp"))
		dr := readDeployResult(t, envDir)
		assert.Equal(t, branch, dr.Name, env)
		assert.Equal(t, commits[branch], dr.Signature, env)
		assert.True(t, dr.DeploySuccess, env)
		assert.Equal(t, cr.dir, dr.GitURL, env)
		assert.Equal(t, sha256File(t, filepath.Join(envDir, "Puppetfile")), dr.PuppetfileChecksum, env)
		assert.False(t, dr.StartedAt.IsZero(), env)
		assert.False(t, dr.FinishedAt.Before(dr.StartedAt), env)
	}
	assert.FileExists(t, filepath.Join(e.env("example_production"), "data", "production.yaml"))
	assert.NoFileExists(t, filepath.Join(e.env("example_main"), "data", "production.yaml"))
	assert.FileExists(t, filepath.Join(e.env("example_feature_foo_bar"), "data", "feature.yaml"))

	// nothing changed upstream, so nothing must be synced again
	res = e.mustRun("-config", cfg, "-info")
	assert.Empty(t, syncedPaths(res.stdout))

	// a new commit is picked up
	cr.checkout("production")
	cr.write("data/production.yaml", "env: production2\n")
	commits["production"] = cr.commit("update production")
	res = e.mustRun("-config", cfg, "-info")
	assert.Equal(t, []string{e.env("example_production")}, syncedPaths(res.stdout))
	assert.Equal(t, "env: production2\n", readFile(t, filepath.Join(e.env("example_production"), "data", "production.yaml")))
	assert.Equal(t, commits["production"], readDeployResult(t, e.env("example_production")).Signature)
}

func TestDeploySingleBranchAndEnvironment(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr, _ := newControlRepo(e)
	cfg := e.config(cr.dir, unreachableForge, "")

	e.mustRun("-config", cfg, "-branch", "production")
	assert.Equal(t, []string{"example_production"}, dirNames(t, e.basedir))

	e.mustRun("-config", cfg, "-environment", "example_main")
	assert.ElementsMatch(t, []string{"example_main", "example_production"}, dirNames(t, e.basedir))

	e.mustRun("-config", cfg, "-branch", "production", "-outputname", "renamed")
	assert.FileExists(t, filepath.Join(e.env("example_renamed"), "data", "production.yaml"))
}

func TestSourcePrefix(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr, _ := newControlRepo(e)
	cfg := e.writeFile("g10k.yaml", `---
cachedir: '`+e.cache+`'
forge_base_url: '`+unreachableForge+`'
sources:
  withprefix:
    remote: '`+cr.dir+`'
    basedir: '`+filepath.Join(e.basedir, "a")+`'
    prefix: true
  customprefix:
    remote: '`+cr.dir+`'
    basedir: '`+filepath.Join(e.basedir, "b")+`'
    prefix: 'custom'
  noprefix:
    remote: '`+cr.dir+`'
    basedir: '`+filepath.Join(e.basedir, "c")+`'
    prefix: false
`)

	e.mustRun("-config", cfg)
	assert.ElementsMatch(t, []string{"withprefix_main", "withprefix_production", "withprefix_feature_foo_bar"}, dirNames(t, filepath.Join(e.basedir, "a")))
	assert.ElementsMatch(t, []string{"custom_main", "custom_production", "custom_feature_foo_bar"}, dirNames(t, filepath.Join(e.basedir, "b")))
	assert.ElementsMatch(t, []string{"main", "production", "feature_foo_bar"}, dirNames(t, filepath.Join(e.basedir, "c")))
}

func TestPurgeLevelDeployment(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr, _ := newControlRepo(e)
	cfg := e.config(cr.dir, unreachableForge, `deploy:
  purge_levels: ['deployment', 'puppetfile']
  deployment_purge_allowlist: ['example_allowed']`)

	e.writeFile(filepath.Join(e.env("example_stale"), "file"), "stale\n")
	e.writeFile(filepath.Join(e.env("example_allowed"), "file"), "allowed\n")
	e.mustRun("-config", cfg)
	assert.NoDirExists(t, e.env("example_stale"))
	assert.DirExists(t, e.env("example_allowed"))
	assert.DirExists(t, e.env("example_main"))

	// deploying a single branch must not purge other environments
	e.writeFile(filepath.Join(e.env("example_stale"), "file"), "stale\n")
	e.mustRun("-config", cfg, "-branch", "main")
	assert.DirExists(t, e.env("example_stale"))

	// without the deployment purge level stale environments are kept
	cfg = e.config(cr.dir, unreachableForge, `deploy:
  purge_levels: ['puppetfile']`)
	e.mustRun("-config", cfg)
	assert.DirExists(t, e.env("example_stale"))
}

// newModuleRepo creates a module repository:
//
//	main: a (tag 1.0.0) - c (annotated tag 2.0.0) - d
//	dev:  a - b
func newModuleRepo(e *testEnv, name string) (*gitRepo, map[string]string) {
	m := e.newRepo(name)
	m.write("VERSION", "a\n")
	m.write("manifests/init.pp", "class "+name+" {}\n")
	commits := map[string]string{"a": m.commit("a")}
	m.git("tag", "1.0.0")
	m.write("VERSION", "c\n")
	commits["c"] = m.commit("c")
	m.git("tag", "-a", "2.0.0", "-m", "release 2.0.0")
	m.write("VERSION", "d\n")
	commits["d"] = m.commit("d")
	m.branch("dev", commits["a"])
	m.write("VERSION", "b\n")
	commits["b"] = m.commit("b")
	m.checkout("main")
	return m, commits
}

// singleEnvControlRepo creates a control repo with only a main branch.
func singleEnvControlRepo(e *testEnv, puppetfile string, files map[string]string) *gitRepo {
	cr := e.newRepo("control")
	cr.write("Puppetfile", puppetfile)
	for name, content := range files {
		cr.write(name, content)
	}
	cr.commit("init")
	return cr
}

func TestPurgeLevelPuppetfile(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	m, _ := newModuleRepo(e, "mymod")
	cr := singleEnvControlRepo(e, "mod 'mymod', :git => '"+m.dir+"'\n", nil)
	cfg := e.config(cr.dir, unreachableForge, "")
	modules := filepath.Join(e.env("example_main"), "modules")

	e.mustRun("-config", cfg)
	e.writeFile(filepath.Join(modules, "bogus", "init.pp"), "bogus\n")
	e.writeFile(filepath.Join(modules, "stray.txt"), "stray\n")
	e.mustRun("-config", cfg)
	assert.Equal(t, []string{"mymod"}, dirNames(t, modules))

	// without the puppetfile purge level unmanaged modules are kept
	cfg = e.config(cr.dir, unreachableForge, `deploy:
  purge_levels: ['deployment']`)
	e.writeFile(filepath.Join(modules, "bogus", "init.pp"), "bogus\n")
	e.mustRun("-config", cfg)
	assert.ElementsMatch(t, []string{"mymod", "bogus"}, dirNames(t, modules))
}

func TestControlRepoFileRemoval(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr := singleEnvControlRepo(e, emptyPuppetfile, map[string]string{
		"data/removed.yaml": "removed: true\n",
		"data/kept.yaml":    "kept: true\n",
	})
	cfg := e.config(cr.dir, unreachableForge, "")
	envDir := e.env("example_main")

	e.mustRun("-config", cfg)
	assert.FileExists(t, filepath.Join(envDir, "data", "removed.yaml"))

	cr.git("rm", "-q", "data/removed.yaml")
	cr.commit("remove file")
	e.mustRun("-config", cfg)
	assert.NoFileExists(t, filepath.Join(envDir, "data", "removed.yaml"))
	assert.FileExists(t, filepath.Join(envDir, "data", "kept.yaml"))
}

func TestPurgeLevelEnvironmentWithAllowlist(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr := singleEnvControlRepo(e, emptyPuppetfile, map[string]string{"data/common.yaml": "a: 1\n"})
	cfg := e.config(cr.dir, unreachableForge, `deploy:
  purge_levels: ['deployment', 'puppetfile', 'environment']
  purge_allowlist: ['keep.txt', 'generated/*.pp']`)
	envDir := e.env("example_main")

	e.mustRun("-config", cfg)
	e.writeFile(filepath.Join(envDir, "junk.txt"), "junk\n")
	e.writeFile(filepath.Join(envDir, "keep.txt"), "keep\n")
	e.writeFile(filepath.Join(envDir, "generated", "type.pp"), "type\n")
	cr.write("data/common.yaml", "a: 2\n")
	cr.commit("change")

	e.mustRun("-config", cfg)
	assert.Equal(t, "a: 2\n", readFile(t, filepath.Join(envDir, "data", "common.yaml")))
	assert.NoFileExists(t, filepath.Join(envDir, "junk.txt"), "unmanaged content must be purged with purge level environment")
	assert.FileExists(t, filepath.Join(envDir, "keep.txt"), "purge_allowlist entries must not be purged")
	assert.FileExists(t, filepath.Join(envDir, "generated", "type.pp"), "purge_allowlist globs must not be purged")
}

func TestUnmanagedEnvironmentContentKeptWithoutEnvironmentPurgeLevel(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr := singleEnvControlRepo(e, emptyPuppetfile, map[string]string{"data/common.yaml": "a: 1\n"})
	// r10k's default purge_levels: unmanaged environment content is not purged
	cfg := e.config(cr.dir, unreachableForge, `deploy:
  purge_levels: ['deployment', 'puppetfile']`)
	envDir := e.env("example_main")

	e.mustRun("-config", cfg)
	e.writeFile(filepath.Join(envDir, "unmanaged.txt"), "unmanaged\n")
	cr.write("data/common.yaml", "a: 2\n")
	cr.commit("change")

	e.mustRun("-config", cfg)
	assert.Equal(t, "a: 2\n", readFile(t, filepath.Join(envDir, "data", "common.yaml")))
	assert.FileExists(t, filepath.Join(envDir, "unmanaged.txt"))
}

func gitModulesPuppetfile(url string, commits map[string]string) string {
	return strings.Join([]string{
		"mod 'mod_branch', :git => '" + url + "', :branch => 'dev'",
		"mod 'mod_ltag', :git => '" + url + "', :tag => '1.0.0'",
		"mod 'mod_atag', :git => '" + url + "', :tag => '2.0.0'",
		"mod 'mod_commit', :git => '" + url + "', :commit => '" + commits["c"] + "'",
		"mod 'mod_ref', :git => '" + url + "', :ref => 'dev'",
		"mod 'mod_default', :git => '" + url + "'",
	}, "\n") + "\n"
}

func TestGitModuleReferences(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	m, commits := newModuleRepo(e, "mymod")
	cr := singleEnvControlRepo(e, gitModulesPuppetfile(m.dir, commits), nil)
	cfg := e.config(cr.dir, unreachableForge, "")
	modules := filepath.Join(e.env("example_main"), "modules")

	e.mustRun("-config", cfg)
	expected := map[string]string{
		"mod_branch":  "b\n",
		"mod_ltag":    "a\n",
		"mod_atag":    "c\n",
		"mod_commit":  "c\n",
		"mod_ref":     "b\n",
		"mod_default": "d\n",
	}
	for mod, version := range expected {
		assert.Equal(t, version, readFile(t, filepath.Join(modules, mod, "VERSION")), mod)
	}

	// a new commit on dev only changes the modules tracking dev
	m.checkout("dev")
	m.write("VERSION", "e\n")
	m.commit("e")
	m.checkout("main")
	res := e.mustRun("-config", cfg, "-info")
	assert.ElementsMatch(t, []string{filepath.Join(modules, "mod_branch"), filepath.Join(modules, "mod_ref")}, syncedPaths(res.stdout))
	assert.Equal(t, "e\n", readFile(t, filepath.Join(modules, "mod_branch", "VERSION")))
	assert.Equal(t, "e\n", readFile(t, filepath.Join(modules, "mod_ref", "VERSION")))
}

func TestGitModuleSecondRunDoesNotResync(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	m, commits := newModuleRepo(e, "mymod")
	cr := singleEnvControlRepo(e, gitModulesPuppetfile(m.dir, commits), nil)
	cfg := e.config(cr.dir, unreachableForge, "")
	modules := filepath.Join(e.env("example_main"), "modules")

	e.mustRun("-config", cfg)
	res := e.mustRun("-config", cfg, "-info")
	synced := syncedPaths(res.stdout)
	for _, mod := range []string{"mod_branch", "mod_ltag", "mod_atag", "mod_commit", "mod_ref", "mod_default"} {
		t.Run(mod, func(t *testing.T) {
			assert.NotContains(t, synced, filepath.Join(modules, mod), "unchanged module must not be synced again")
		})
	}
}

func TestCloneGitModules(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	m, commits := newModuleRepo(e, "mymod")
	cr := singleEnvControlRepo(e, "mod 'cloned', :git => '"+m.dir+"', :branch => 'dev'\nmod 'cloned_atag', :git => '"+m.dir+"', :tag => '2.0.0'\n", nil)
	cfg := e.config(cr.dir, unreachableForge, "clone_git_modules: true")
	moduleDir := filepath.Join(e.env("example_main"), "modules", "cloned")
	tagModuleDir := filepath.Join(e.env("example_main"), "modules", "cloned_atag")
	head := func() string {
		return (&gitRepo{t: t, dir: moduleDir, home: e.home}).git("rev-parse", "HEAD")
	}

	e.mustRun("-config", cfg)
	assert.DirExists(t, filepath.Join(moduleDir, ".git"))
	assert.Equal(t, commits["b"], head())
	assert.Equal(t, commits["c"], (&gitRepo{t: t, dir: tagModuleDir, home: e.home}).git("rev-parse", "HEAD"))

	t.Run("second run does not resync", func(t *testing.T) {
		res := e.mustRun("-config", cfg, "-info")
		assert.NotContains(t, syncedPaths(res.stdout), moduleDir)
		assert.NotContains(t, syncedPaths(res.stdout), tagModuleDir)
	})

	m.checkout("dev")
	m.write("VERSION", "e\n")
	newCommit := m.commit("e")
	m.checkout("main")

	t.Run("dryrun does not modify the module", func(t *testing.T) {
		res := e.run("-config", cfg, "-dryrun")
		assert.Equal(t, 1, res.code, res.output())
		assert.Equal(t, commits["b"], head())
	})

	e.mustRun("-config", cfg)
	assert.Equal(t, newCommit, head())
	assert.Equal(t, "e\n", readFile(t, filepath.Join(moduleDir, "VERSION")))
}

func TestControlBranchAndDefaultBranch(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	m := e.newRepo("linked")
	m.write("VERSION", "main\n")
	m.commit("main")
	m.branch("production", "main")
	m.write("VERSION", "production\n")
	m.commit("production")
	m.checkout("main")

	linked := "mod 'linked', :git => '" + m.dir + "', :branch => :control_branch\n"
	fallback := "mod 'fallback', :git => '" + m.dir + "', :branch => :control_branch, :default_branch => 'main'\n"
	cr := e.newRepo("control")
	cr.write("Puppetfile", linked+fallback)
	cr.commit("main")
	cr.branch("production", "main")
	cr.write("data/production.yaml", "env: production\n")
	cr.commit("production")
	cr.branch("feature", "main")
	cr.write("Puppetfile", fallback)
	cr.commit("feature")
	cr.checkout("main")
	cfg := e.config(cr.dir, unreachableForge, "")

	e.mustRun("-config", cfg)
	assert.Equal(t, "main\n", readFile(t, filepath.Join(e.env("example_main"), "modules", "linked", "VERSION")))
	assert.Equal(t, "production\n", readFile(t, filepath.Join(e.env("example_production"), "modules", "linked", "VERSION")))
	assert.Equal(t, "production\n", readFile(t, filepath.Join(e.env("example_production"), "modules", "fallback", "VERSION")))
	assert.Equal(t, "main\n", readFile(t, filepath.Join(e.env("example_feature"), "modules", "fallback", "VERSION")))
}

func TestInstallPathAndLocalModules(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	m, _ := newModuleRepo(e, "ipmod")
	cr := singleEnvControlRepo(e, "mod 'ipmod', :git => '"+m.dir+"', :install_path => 'site'\nmod 'localmod', :local => true\n", map[string]string{
		"modules/localmod/manifests/init.pp": "class localmod {}\n",
	})
	cfg := e.config(cr.dir, unreachableForge, "")
	envDir := e.env("example_main")

	e.mustRun("-config", cfg)
	assert.Equal(t, "d\n", readFile(t, filepath.Join(envDir, "site", "ipmod", "VERSION")))
	assert.NoDirExists(t, filepath.Join(envDir, "modules", "ipmod"))
	assert.FileExists(t, filepath.Join(envDir, "modules", "localmod", "manifests", "init.pp"))

	e.writeFile(filepath.Join(envDir, "modules", "bogus", "init.pp"), "bogus\n")
	e.mustRun("-config", cfg)
	assert.NoDirExists(t, filepath.Join(envDir, "modules", "bogus"))
	assert.FileExists(t, filepath.Join(envDir, "modules", "localmod", "manifests", "init.pp"))
	assert.FileExists(t, filepath.Join(envDir, "site", "ipmod", "VERSION"))
}

func TestForgeModules(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	forge := newFakeForge(t)
	forge.publish("acme-pinned", "1.0.0")
	forge.publish("acme-pinned", "1.1.0")
	forge.publish("acme-newest", "2.0.0")
	cr := singleEnvControlRepo(e, "mod 'acme/pinned', '1.0.0'\nmod 'acme-newest', :latest\n", nil)
	cfg := e.config(cr.dir, forge.url(), "")
	modules := filepath.Join(e.env("example_main"), "modules")

	e.mustRun("-config", cfg)
	assert.Equal(t, "1.0.0", moduleVersion(t, filepath.Join(modules, "pinned")))
	assert.Equal(t, "2.0.0", moduleVersion(t, filepath.Join(modules, "newest")))

	res := e.mustRun("-config", cfg, "-info")
	assert.Empty(t, syncedPaths(res.stdout))
	assert.Equal(t, 1, forge.downloadCount("acme-pinned", "1.0.0"))
	assert.Equal(t, 1, forge.downloadCount("acme-newest", "2.0.0"))

	forge.publish("acme-newest", "2.1.0")
	res = e.mustRun("-config", cfg, "-info")
	assert.Equal(t, []string{filepath.Join(modules, "newest")}, syncedPaths(res.stdout))
	assert.Equal(t, "2.1.0", moduleVersion(t, filepath.Join(modules, "newest")))
	assert.Equal(t, "1.0.0", moduleVersion(t, filepath.Join(modules, "pinned")))
}

func TestPostrunCommand(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr, _ := newControlRepo(e)
	log := filepath.Join(e.root, "postrun.log")
	script := e.writeFile("postrun.sh", "#!/bin/sh\necho \"$@\" >> '"+log+"'\n")
	require.NoError(t, os.Chmod(script, 0o755))
	cfg := e.config(cr.dir, unreachableForge, "postrun: ['"+script+"', '$modifiedenvs']")

	e.mustRun("-config", cfg)
	lines := strings.Split(strings.TrimSuffix(readFile(t, log), "\n"), "\n")
	require.Len(t, lines, 1)
	assert.ElementsMatch(t, []string{"example_main", "example_production", "example_feature_foo_bar"}, strings.Fields(lines[0]))

	e.mustRun("-config", cfg)
	lines = strings.Split(strings.TrimSuffix(readFile(t, log), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Empty(t, strings.Fields(lines[1]))
}

func TestConfigDryRun(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr, _ := newControlRepo(e)
	cfg := e.config(cr.dir, unreachableForge, "")

	res := e.run("-config", cfg, "-dryrun")
	assert.Equal(t, 1, res.code, res.output())
	assert.NoDirExists(t, e.env("example_main"))

	e.mustRun("-config", cfg)
	e.mustRun("-config", cfg, "-dryrun")
}

func TestValidate(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	cr, _ := newControlRepo(e)
	cfg := e.config(cr.dir, unreachableForge, "")

	res := e.mustRun("-config", cfg, "-validate")
	assert.Contains(t, res.stdout, "Configuration successfully parsed.")
	assert.NoDirExists(t, e.env("example_main"), "-validate must not deploy anything")

	invalid := e.writeFile("invalid.yaml", "---\ncachedir: '"+e.cache+"'\nsources:\n  example:\n    basedir: '"+e.basedir+"'\n")
	res = e.run("-config", invalid, "-validate")
	assert.Equal(t, 1, res.code, res.output())
	assert.Contains(t, res.output(), "config setting remote is not set for source example")

	e.writeFile(filepath.Join(e.work, "Puppetfile"), "mod 'broken', :nonsense => 'x'\n")
	res = e.run("-puppetfile", "-validate")
	assert.Equal(t, 1, res.code, res.output())
	assert.NoDirExists(t, filepath.Join(e.work, "modules"))
}
