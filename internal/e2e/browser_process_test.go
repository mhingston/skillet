package e2e

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var runBrowserProcessAcceptance = flag.Bool("skillet-browser-process", false, "run the real-process headless browser acceptance test")

// TestOfflineM3BrowserProcessAcceptance proves the shipped Go binary serves the
// browser adapter correctly when driven by a real headless browser. The richer
// M3 browser fixtures exercise scoped data and mutations in-process; this test
// deliberately focuses on the process/browser boundary without introducing a
// browser automation library or Node runtime.
func TestOfflineM3BrowserProcessAcceptance(t *testing.T) {
	if !*runBrowserProcessAcceptance {
		t.Skip("enable with -skillet-browser-process; cmd/skillet-verify runs this gate")
	}

	browser, err := browserExecutable()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binaryName := "skillet"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binary := filepath.Join(root, binaryName)
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/skillet")
	build.Dir = repoRoot
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build skillet process fixture: %v\n%s", buildErr, output)
	}

	listen, err := freeLoopbackAddress()
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listen
	configPath := filepath.Join(root, "skillet.yaml")
	config := fmt.Sprintf(`server:
  listen: %q
  data_dir: %q
  public_base_url: %q
  mcp_path: "/mcp"
  shutdown_timeout: "2s"
  max_request_body_bytes: 1048576
organization:
  id: "m3-browser"
  display_name: "M3 Browser Acceptance"
auth:
  mode: "development"
packages:
  enabled: true
search:
  default_limit: 5
  max_limit: 10
repositories: []
`, listen, filepath.Join(root, "data"), baseURL)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	server := exec.CommandContext(serverCtx, binary, "--config", configPath)
	server.Dir = repoRoot
	serverOutput := &strings.Builder{}
	server.Stdout = serverOutput
	server.Stderr = serverOutput
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	defer func() {
		stopServer()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if server.Process != nil {
				_ = server.Process.Kill()
			}
		}
	}()

	if err := waitForBrowserSurface(ctx, baseURL+"/ui/catalogue"); err != nil {
		t.Fatalf("local Skillet process never became ready: %v\n%s", err, serverOutput.String())
	}

	for _, journey := range []struct {
		path string
		want []string
	}{
		{path: "/ui/catalogue", want: []string{"Find reusable capabilities", "/ui/assets/skillet.css", "/ui/assets/htmx.min.js"}},
		{path: "/ui/knowledge", want: []string{"Knowledge", "Search"}},
	} {
		args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--dump-dom", baseURL + journey.path}
		command := exec.CommandContext(ctx, browser, args...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("headless browser %s: %v\n%s", journey.path, err, output)
		}
		dom := string(output)
		for _, want := range journey.want {
			if !strings.Contains(dom, want) {
				t.Fatalf("headless browser %s missing %q\n%s", journey.path, want, dom)
			}
		}
	}
}

func freeLoopbackAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("reserve browser fixture address: %w", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("release browser fixture address: %w", err)
	}
	return address, nil
}

func waitForBrowserSurface(ctx context.Context, target string) error {
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func browserExecutable() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("SKILLET_BROWSER_BIN")); configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("SKILLET_BROWSER_BIN %q: %w", configured, err)
		}
		return configured, nil
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	for _, path := range browserPlatformPaths() {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("headless Chrome/Chromium not found; install Chrome/Chromium or set SKILLET_BROWSER_BIN")
}

func browserPlatformPaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	case "windows":
		var paths []string
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if root != "" {
				paths = append(paths, filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
		return paths
	default:
		return nil
	}
}
