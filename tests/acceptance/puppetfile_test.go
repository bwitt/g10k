//go:build unix

package acceptance

import (
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPuppetfileModeModuledir(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	forge := newFakeForge(t)
	forge.publish("acme-pinned", "1.0.0")
	m, _ := newModuleRepo(e, "mymod")
	e.writeFile(filepath.Join(e.work, "custom", "Puppetfile.custom"), "forge.baseUrl "+forge.url()+"\n"+
		"mod 'acme/pinned', '1.0.0'\n"+
		"mod 'mymod', :git => '"+m.dir+"', :branch => 'dev'\n")

	e.mustRun("-puppetfile", "-puppetfilelocation", "custom/Puppetfile.custom", "-moduledir", "vendor")
	assert.Equal(t, "1.0.0", moduleVersion(t, filepath.Join(e.work, "vendor", "pinned")))
	assert.Equal(t, "b\n", readFile(t, filepath.Join(e.work, "vendor", "mymod", "VERSION")))
	assert.NoDirExists(t, filepath.Join(e.work, "modules"))
	assert.DirExists(t, filepath.Join(e.home, ".cache", "g10k", "forge"), "default cache dir must be under the user cache dir")

	res := e.mustRun("-puppetfile", "-puppetfilelocation", "custom/Puppetfile.custom", "-moduledir", "vendor", "-info")
	assert.Empty(t, syncedPaths(res.stdout))
}

func TestPuppetfileDryRun(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	forge := newFakeForge(t)
	forge.publish("acme-pinned", "1.0.0")
	m, _ := newModuleRepo(e, "mymod")
	e.writeFile(filepath.Join(e.work, "Puppetfile"), "forge.baseUrl "+forge.url()+"\n"+
		"mod 'acme/pinned', '1.0.0'\n"+
		"mod 'mymod', :git => '"+m.dir+"', :branch => 'dev'\n")
	modules := filepath.Join(e.work, "modules")

	res := e.run("-puppetfile", "-dryrun")
	assert.Equal(t, 1, res.code, "pending changes must be signalled with exit code 1\n"+res.output())
	assert.NoDirExists(t, filepath.Join(modules, "pinned"))
	assert.NoDirExists(t, filepath.Join(modules, "mymod"))

	e.mustRun("-puppetfile")
	res = e.run("-puppetfile", "-dryrun")
	assert.Equal(t, 0, res.code, "no pending changes must result in exit code 0\n"+res.output())

	m.checkout("dev")
	m.write("VERSION", "e\n")
	m.commit("e")
	m.checkout("main")
	res = e.run("-puppetfile", "-dryrun")
	assert.Equal(t, 1, res.code, res.output())
	assert.FileExists(t, filepath.Join(modules, "mymod", "VERSION"), "-dryrun must not remove an outdated module")

	e.mustRun("-puppetfile")
	assert.Equal(t, "e\n", readFile(t, filepath.Join(modules, "mymod", "VERSION")))
}

func TestForgeChecksumMatch(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	forge := newFakeForge(t)
	forge.publish("acme-good", "1.0.0")
	e.writeFile(filepath.Join(e.work, "Puppetfile"), "forge.baseUrl "+forge.url()+"\n"+
		"mod 'acme/good', '1.0.0', :sha256sum => '"+forge.tarballSHA256("acme-good", "1.0.0")+"'\n")

	e.mustRun("-puppetfile")
	assert.Equal(t, "1.0.0", moduleVersion(t, filepath.Join(e.work, "modules", "good")))
	e.mustRun("-puppetfile", "-checksum")
}

func TestForgeChecksumMismatch(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	forge := newFakeForge(t)
	forge.publish("acme-bad", "1.0.0")
	e.writeFile(filepath.Join(e.work, "Puppetfile"), "forge.baseUrl "+forge.url()+"\n"+
		"mod 'acme/bad', '1.0.0', :sha256sum => '"+strings.Repeat("0", 64)+"'\n")
	moduleDir := filepath.Join(e.work, "modules", "bad")

	res := e.run("-puppetfile")
	assert.NotEqual(t, 0, res.code, res.output())
	assert.Contains(t, res.output(), "does not match expected sha256sum")
	assert.NoDirExists(t, moduleDir)

	// the cached download must not be trusted on the next run either
	res = e.run("-puppetfile")
	assert.NotEqual(t, 0, res.code, res.output())
	assert.NoDirExists(t, moduleDir)
}

func TestCheck4Update(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	forge := newFakeForge(t)
	forge.publish("acme-pinned", "1.0.0")
	forge.publish("acme-pinned", "1.1.0")
	e.writeFile(filepath.Join(e.work, "Puppetfile"), "forge.baseUrl "+forge.url()+"\nmod 'acme/pinned', '1.0.0'\n")
	moduleDir := filepath.Join(e.work, "modules", "pinned")

	e.mustRun("-puppetfile")
	res := e.run("-puppetfile", "-check4update")
	assert.Contains(t, res.stdout, "ATTENTION: Forge module: pinned latest: 1.1.0 currently deployed: 1.0.0", res.output())
	assert.Equal(t, "1.0.0", moduleVersion(t, moduleDir), "-check4update must not change deployed modules")
}

// recordingProxy is a forward HTTP proxy that records the hosts it served.
type recordingProxy struct {
	mu    sync.Mutex
	hosts map[string]int
}

func (p *recordingProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.hosts[r.URL.Host]++
	p.mu.Unlock()
	r.RequestURI = ""
	resp, err := (&http.Transport{}).RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *recordingProxy) count(host string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hosts[host]
}

// A NO_PROXY match for one repository must not disable the proxy for others.
func TestNoProxyIsScopedToMatchingRepository(t *testing.T) {
	t.Parallel()
	e := newTestEnv(t)
	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)

	served := filepath.Join(e.root, "served")
	m, _ := newModuleRepo(e, "mymod")
	cr := singleEnvControlRepo(e, "", nil)
	for name, repo := range map[string]*gitRepo{"mymod.git": m, "control.git": cr} {
		repo.git("clone", "-q", "--bare", repo.dir, filepath.Join(served, name))
	}
	gitServer := httptest.NewServer(&cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Root: "/",
		Env:  []string{"GIT_PROJECT_ROOT=" + served, "GIT_HTTP_EXPORT_ALL=1"},
	})
	t.Cleanup(gitServer.Close)
	port := strings.TrimPrefix(gitServer.URL, "http://127.0.0.1:")
	proxy := &recordingProxy{hosts: map[string]int{}}
	proxyServer := httptest.NewServer(proxy)
	t.Cleanup(proxyServer.Close)

	// the control repo is reached via localhost (excluded by NO_PROXY),
	// the module via 127.0.0.1 which has to go through the proxy
	controlURL := "http://localhost:" + port + "/control.git"
	moduleURL := "http://127.0.0.1:" + port + "/mymod.git"
	cr.write("Puppetfile", "mod 'mymod', :git => '"+moduleURL+"', :branch => 'dev'\n")
	cr.commit("use http module")
	cr.git("push", "-q", filepath.Join(served, "control.git"), "main")
	cfg := e.config(controlURL, unreachableForge, "")
	e.extraEnv = []string{"http_proxy=" + proxyServer.URL, "NO_PROXY=localhost", "no_proxy=localhost"}

	e.mustRun("-config", cfg)
	assert.Equal(t, "b\n", readFile(t, filepath.Join(e.env("example_main"), "modules", "mymod", "VERSION")))
	assert.Equal(t, 0, proxy.count("localhost:"+port), "NO_PROXY host must not use the proxy")
	assert.Positive(t, proxy.count("127.0.0.1:"+port), "repositories not matching NO_PROXY must use the proxy")
}
