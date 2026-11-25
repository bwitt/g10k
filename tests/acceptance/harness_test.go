//go:build unix

// Package acceptance runs the compiled g10k binary end to end against local
// git repositories and a fake Forge, without any network access.
package acceptance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// unreachableForge is used when a test does not expect any Forge traffic, so
// that an unexpected request fails fast instead of reaching the real Forge.
const unreachableForge = "http://127.0.0.1:1"

var g10kBinary string

func TestMain(m *testing.M) {
	syscall.Umask(0o022)
	dir, err := os.MkdirTemp("", "g10k-acceptance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	g10kBinary = filepath.Join(dir, "g10k")
	build := exec.Command("go", "build", "-o", g10kBinary, "./cmd/g10k")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building g10k failed: %s\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// testEnv is an isolated root directory with its own HOME, cache, basedir and
// working directory for a single test.
type testEnv struct {
	t        *testing.T
	root     string
	home     string
	cache    string
	basedir  string
	work     string
	extraEnv []string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	e := &testEnv{
		t:       t,
		root:    root,
		home:    filepath.Join(root, "home"),
		cache:   filepath.Join(root, "cache"),
		basedir: filepath.Join(root, "environments"),
		work:    filepath.Join(root, "work"),
	}
	for _, d := range []string{e.home, e.work} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	return e
}

func baseEnviron(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"LANG=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=maintenance.auto",
		"GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=gc.auto",
		"GIT_CONFIG_VALUE_1=0",
	}
}

type result struct {
	code   int
	stdout string
	stderr string
}

func (r result) output() string {
	return "exit code: " + fmt.Sprint(r.code) + "\nstdout:\n" + r.stdout + "\nstderr:\n" + r.stderr
}

// run executes the g10k binary in the working directory of the test.
func (e *testEnv) run(args ...string) result {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, g10kBinary, args...)
	cmd.Dir = e.work
	cmd.Env = append(baseEnviron(e.home), e.extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		res.code = exitErr.ExitCode()
	case err != nil:
		e.t.Fatalf("running g10k %v failed: %s", args, err)
	}
	if ctx.Err() != nil {
		e.t.Fatalf("g10k %v timed out:\n%s", args, res.output())
	}
	return res
}

// mustRun executes g10k and fails the test unless it exits with code 0.
func (e *testEnv) mustRun(args ...string) result {
	e.t.Helper()
	res := e.run(args...)
	require.Equal(e.t, 0, res.code, res.output())
	return res
}

func (e *testEnv) writeFile(path string, content string) string {
	e.t.Helper()
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.root, path)
	}
	require.NoError(e.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(e.t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// config writes a g10k config with a single source "example" (prefix: true)
// and returns its path. extra is inserted verbatim at the top level.
func (e *testEnv) config(remote string, forgeURL string, extra string) string {
	e.t.Helper()
	return e.writeFile("g10k.yaml", fmt.Sprintf(`---
cachedir: '%s'
forge_base_url: '%s'
%s
sources:
  example:
    remote: '%s'
    basedir: '%s'
    prefix: true
`, e.cache, forgeURL, extra, remote, e.basedir))
}

func (e *testEnv) env(name string) string {
	return filepath.Join(e.basedir, name)
}

func (e *testEnv) newRepo(name string) *gitRepo {
	e.t.Helper()
	return newGitRepo(e.t, filepath.Join(e.root, "repos", name), e.home)
}

// syncedPaths returns the paths g10k reported as "Need to sync" (needs -info).
func syncedPaths(stdout string) []string {
	paths := []string{}
	for _, line := range strings.Split(stdout, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "Need to sync "); ok {
			paths = append(paths, p)
		}
	}
	return paths
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(readFile(t, path)))
	return hex.EncodeToString(sum[:])
}

type deployResult struct {
	Name               string    `json:"name"`
	Signature          string    `json:"signature"`
	StartedAt          time.Time `json:"started_at"`
	FinishedAt         time.Time `json:"finished_at"`
	DeploySuccess      bool      `json:"deploy_success"`
	PuppetfileChecksum string    `json:"puppetfile_checksum"`
	GitDir             string    `json:"git_dir"`
	GitURL             string    `json:"git_url"`
}

func readDeployResult(t *testing.T, envDir string) deployResult {
	t.Helper()
	var dr deployResult
	require.NoError(t, json.Unmarshal([]byte(readFile(t, filepath.Join(envDir, ".g10k-deploy.json"))), &dr))
	return dr
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// gitRepo is a local non-bare git repository used as a remote by g10k.
type gitRepo struct {
	t    *testing.T
	dir  string
	home string
}

func newGitRepo(t *testing.T, dir string, home string) *gitRepo {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	r := &gitRepo{t: t, dir: dir, home: home}
	r.git("init", "-q")
	r.git("checkout", "-q", "-b", "main")
	return r
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(baseEnviron(r.home),
		"GIT_AUTHOR_NAME=g10k acceptance",
		"GIT_AUTHOR_EMAIL=acceptance@example.invalid",
		"GIT_COMMITTER_NAME=g10k acceptance",
		"GIT_COMMITTER_EMAIL=acceptance@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(r.t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func (r *gitRepo) write(name string, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	require.NoError(r.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(r.t, os.WriteFile(path, []byte(content), 0o644))
}

// commit commits all changes and returns the new commit hash.
func (r *gitRepo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *gitRepo) checkout(branch string) {
	r.t.Helper()
	r.git("checkout", "-q", branch)
}

func (r *gitRepo) branch(branch string, from string) {
	r.t.Helper()
	r.git("checkout", "-q", "-b", branch, from)
}

// fakeForge implements the subset of the Forge v3 API that g10k uses.
type fakeForge struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	releases  map[string]map[string][]byte
	latest    map[string]string
	downloads map[string]int
}

func newFakeForge(t *testing.T) *fakeForge {
	t.Helper()
	f := &fakeForge{
		t:         t,
		releases:  map[string]map[string][]byte{},
		latest:    map[string]string{},
		downloads: map[string]int{},
	}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeForge) url() string {
	return f.srv.URL
}

// publish adds a release of slug (e.g. "acme-ntp") and makes it the latest.
func (f *fakeForge) publish(slug string, version string) {
	f.t.Helper()
	tarball := forgeTarball(f.t, slug, version)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.releases[slug] == nil {
		f.releases[slug] = map[string][]byte{}
	}
	f.releases[slug][version] = tarball
	f.latest[slug] = version
}

func (f *fakeForge) downloadCount(slug string, version string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloads[slug+"-"+version]
}

func releaseJSON(tarball []byte, version string) map[string]any {
	sum := md5.Sum(tarball)
	return map[string]any{
		"version":   version,
		"file_md5":  hex.EncodeToString(sum[:]),
		"file_size": len(tarball),
	}
}

// lookup splits "acme-ntp-1.0.0" into a known slug and version.
func (f *fakeForge) lookup(name string) (string, string, []byte, bool) {
	for slug, versions := range f.releases {
		if version, ok := strings.CutPrefix(name, slug+"-"); ok {
			if tarball, ok := versions[version]; ok {
				return slug, version, tarball, true
			}
		}
	}
	return "", "", nil, false
}

func (f *fakeForge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/v3/modules/"):
		slug := strings.TrimPrefix(path, "/v3/modules/")
		version, ok := f.latest[slug]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"slug":            slug,
			"current_release": releaseJSON(f.releases[slug][version], version),
			"deprecated_at":   nil,
		})
	case strings.HasPrefix(path, "/v3/releases/"):
		_, version, tarball, ok := f.lookup(strings.TrimPrefix(path, "/v3/releases/"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, releaseJSON(tarball, version))
	case strings.HasPrefix(path, "/v3/files/") && strings.HasSuffix(path, ".tar.gz"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/v3/files/"), ".tar.gz")
		_, _, tarball, ok := f.lookup(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		f.downloads[name]++
		_, _ = w.Write(tarball)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// forgeTarball builds a Forge release archive with metadata.json.
func forgeTarball(t *testing.T, slug string, version string) []byte {
	t.Helper()
	top := slug + "-" + version + "/"
	author, name, _ := strings.Cut(slug, "-")
	metadata := fmt.Sprintf(`{"name": %q, "version": %q, "author": %q}`, slug, version, author)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, dir := range []string{top, top + "manifests/"} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir, Mode: 0o755, ModTime: time.Now()}))
	}
	files := []struct{ name, content string }{
		{top + "metadata.json", metadata},
		{top + "manifests/init.pp", "class " + name + " {}\n"},
	}
	for _, file := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: file.name, Mode: 0o644, Size: int64(len(file.content)), ModTime: time.Now()}))
		_, err := tw.Write([]byte(file.content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func moduleVersion(t *testing.T, moduleDir string) string {
	t.Helper()
	var metadata struct {
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal([]byte(readFile(t, filepath.Join(moduleDir, "metadata.json"))), &metadata))
	return metadata.Version
}

func (f *fakeForge) tarballSHA256(slug string, version string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256(f.releases[slug][version])
	return hex.EncodeToString(sum[:])
}
