package g10k

import (
	"os"
	"strings"
	"testing"
)

func TestExecuteCommandDisableHttpProxy(t *testing.T) {
	rt := NewRuntime(Options{})
	t.Setenv("http_proxy", "http://proxy.domain.tld:3128")
	t.Setenv("HTTPS_PROXY", "http://proxy.domain.tld:3128")

	er := rt.executeCommand("env", "", 5, false, true)
	if er.returnCode != 0 {
		t.Fatalf("executeCommand() failed with return code %d and output %s", er.returnCode, er.output)
	}
	if strings.Contains(er.output, "proxy.domain.tld") {
		t.Errorf("executeCommand() should not pass the proxy settings to the command, but got %s", er.output)
	}
	if os.Getenv("http_proxy") == "" || os.Getenv("HTTPS_PROXY") == "" {
		t.Error("executeCommand() should not unset the proxy settings of the g10k process")
	}

	er = rt.executeCommand("env", "", 5, false, false)
	if !strings.Contains(er.output, "http_proxy=http://proxy.domain.tld:3128") || !strings.Contains(er.output, "HTTPS_PROXY=http://proxy.domain.tld:3128") {
		t.Errorf("executeCommand() should pass the proxy settings to commands that do not match NO_PROXY, but got %s", er.output)
	}
}
