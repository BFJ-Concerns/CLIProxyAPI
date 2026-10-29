package managementasset

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// recordingProxy counts every request routed through it, so a sync given its
// URL as the proxy shows whether it reached for the network.
func recordingProxy(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	return server.URL, &hits
}

func TestBundledManagementHTMLIsPanelBuild(t *testing.T) {
	if !bytes.HasPrefix(bundledManagementHTML, []byte("<!doctype html>")) {
		t.Fatalf("bundled panel is not an HTML document (%d bytes)", len(bundledManagementHTML))
	}
}

func TestEnsureLatestManagementHTMLInstallsBundledPanelWithoutNetwork(t *testing.T) {
	proxyURL, hits := recordingProxy(t)
	staticDir := filepath.Join(t.TempDir(), "static")

	if !EnsureLatestManagementHTML(context.Background(), staticDir, proxyURL, "https://github.com/router-for-me/Cli-Proxy-API-Management-Center") {
		t.Fatal("EnsureLatestManagementHTML reported no asset")
	}

	got, errRead := os.ReadFile(filepath.Join(staticDir, ManagementFileName))
	if errRead != nil {
		t.Fatalf("read installed asset: %v", errRead)
	}
	if !bytes.Equal(got, bundledManagementHTML) {
		t.Fatalf("installed asset differs from the bundled panel (%d bytes, want %d)", len(got), len(bundledManagementHTML))
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("sync sent %d request(s) through the proxy, want none", n)
	}
}

func TestEnsureLatestManagementHTMLReplacesDifferingCopy(t *testing.T) {
	proxyURL, hits := recordingProxy(t)
	staticDir := t.TempDir()
	localPath := filepath.Join(staticDir, ManagementFileName)
	if errWrite := os.WriteFile(localPath, []byte("<html>other panel</html>"), 0o644); errWrite != nil {
		t.Fatalf("seed local asset: %v", errWrite)
	}

	if !EnsureLatestManagementHTML(context.Background(), staticDir, proxyURL, "") {
		t.Fatal("EnsureLatestManagementHTML reported no asset")
	}

	got, errRead := os.ReadFile(localPath)
	if errRead != nil {
		t.Fatalf("read installed asset: %v", errRead)
	}
	if !bytes.Equal(got, bundledManagementHTML) {
		t.Fatal("differing local copy was not replaced by the bundled panel")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("sync sent %d request(s) through the proxy, want none", n)
	}
}
