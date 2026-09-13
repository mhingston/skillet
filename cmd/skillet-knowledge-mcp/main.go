package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mhingston/skillet/internal/knowledge"
	"github.com/mhingston/skillet/internal/knowledgemcp"
)

func main() {
	dataDir := flag.String("data-dir", "", "knowledge data directory")
	bundleRoot := flag.String("bundle-root", "", "optional local OKF bundle to reconcile before serving")
	bundleID := flag.String("bundle-id", "local-okf", "stable OKF bundle identity")
	locator := flag.String("locator", "", "source locator recorded in provenance")
	revision := flag.String("revision", "", "source revision recorded in provenance")
	listen := flag.String("listen", "127.0.0.1:8081", "HTTP listen address")
	mcpPath := flag.String("mcp-path", "/mcp", "Streamable HTTP MCP path")
	flag.Parse()
	if *dataDir == "" {
		fmt.Fprintln(os.Stderr, "skillet-knowledge-mcp: -data-dir is required")
		os.Exit(2)
	}
	if *mcpPath == "" || (*mcpPath)[0] != '/' {
		fmt.Fprintln(os.Stderr, "skillet-knowledge-mcp: -mcp-path must start with /")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	service, err := knowledge.Open(ctx, *dataDir, knowledge.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "skillet-knowledge-mcp:", err)
		os.Exit(2)
	}
	defer service.Close()
	if *bundleRoot != "" {
		if _, err := service.ReindexOKF(ctx, []knowledge.OKFBundle{{ID: *bundleID, Root: *bundleRoot, Locator: *locator, Revision: *revision}}); err != nil {
			fmt.Fprintln(os.Stderr, "skillet-knowledge-mcp:", err)
			os.Exit(2)
		}
	}

	mux := http.NewServeMux()
	mux.Handle(*mcpPath, knowledgemcp.Handler(service, 1<<20))
	server := &http.Server{Addr: *listen, Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "skillet-knowledge-mcp:", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}
