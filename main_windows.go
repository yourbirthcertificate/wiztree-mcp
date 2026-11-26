package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is overridden at build time with -ldflags "-X main.version=1.2.3".
var version = "1.0.0"

func tool(name, description string, readOnly, idempotent, openWorld bool) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: description, Annotations: &mcp.ToolAnnotations{
		ReadOnlyHint:    readOnly,
		DestructiveHint: new(bool),
		IdempotentHint:  idempotent,
		OpenWorldHint:   &openWorld,
	}}
}

func server(jobs *scans) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "wiztree-mcp", Version: version}, &mcp.ServerOptions{
		Instructions: "Use scan on the requested Windows path. If it returns running, call get_scan with its scan_id until complete. Each report includes the root totals, largest files anywhere beneath it, immediate child folders, and file extensions. Scan a child folder to drill down.",
	})
	mcp.AddTool(s, tool("list_drives", "List Windows drive letters and types. Includes capacity and available bytes for accessible fixed drives.", true, true, false),
		func(ctx context.Context, r *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, Drives, error) {
			out, err := drives()
			return nil, out, err
		})
	mcp.AddTool(s, tool("scan", "Analyze a Windows drive, folder, or network share with WizTree. Small scans return a report immediately. Longer scans return a scan_id for get_scan.", true, false, true),
		func(ctx context.Context, r *mcp.CallToolRequest, in ScanInput) (*mcp.CallToolResult, ScanResult, error) {
			out, err := jobs.start(ctx, in)
			return nil, out, err
		})
	mcp.AddTool(s, tool("get_scan", "Wait briefly for a previous scan and retrieve its status and report. Retry while running.", true, true, false),
		func(ctx context.Context, r *mcp.CallToolRequest, in GetInput) (*mcp.CallToolResult, ScanResult, error) {
			seconds := 5
			if in.Wait != nil {
				seconds = *in.Wait
			}
			if seconds < 0 || seconds > 15 {
				return nil, ScanResult{}, errors.New("wait_seconds must be between 0 and 15")
			}
			j, err := jobs.find(in.ID)
			if err != nil {
				return nil, ScanResult{}, err
			}
			out, err := j.result(ctx, time.Duration(seconds)*time.Second)
			return nil, out, err
		})
	mcp.AddTool(s, tool("cancel_scan", "Stop the specified background scan. Completed reports remain available.", false, true, false),
		func(ctx context.Context, r *mcp.CallToolRequest, in ScanID) (*mcp.CallToolResult, ScanResult, error) {
			j, err := jobs.find(in.ID)
			if err != nil {
				return nil, ScanResult{}, err
			}
			j.cancel()
			out, err := j.result(ctx, time.Second)
			return nil, out, err
		})
	return s
}

func config(client string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if client == "codex" {
		// A JSON quoted Windows path is also a TOML basic string.
		path, err := json.Marshal(exe)
		if err != nil {
			return err
		}
		fmt.Printf("[mcp_servers.wiztree]\ncommand = %s\n", path)
		return nil
	}
	key := "mcpServers"
	entry := map[string]any{"command": exe, "args": []string{}}
	switch client {
	case "json", "claude":
	case "cursor":
		entry["type"] = "stdio"
	case "vscode":
		key = "servers"
		entry["type"] = "stdio"
	default:
		return errors.New("config supports json, claude, cursor, vscode and codex")
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{key: map[string]any{"wiztree": entry}})
}

func serveHTTP(ctx context.Context, s *mcp.Server, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	local := fmt.Sprintf("localhost:%d", port)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if (r.Host != addr && r.Host != local) || (origin != "" && origin != "http://"+addr && origin != "http://"+local) {
			http.Error(w, "local MCP clients only", http.StatusForbidden)
			return
		}
		handler.ServeHTTP(w, r)
	})
	h := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); _ = h.Close() }()
	log.Printf("WizTree MCP listening on http://%s/mcp", addr)
	err := h.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func run() error {
	client := flag.String("config", "", "print registration settings for json, claude, cursor, vscode or codex and exit")
	httpMode := flag.Bool("http", false, "serve Streamable HTTP on loopback; default transport is stdio")
	port := flag.Int("port", 57421, "loopback port for HTTP mode")
	flag.Parse()
	if *client != "" {
		return config(*client)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	jobs := &scans{ctx: ctx}
	defer func() {
		cancel()
		// A starter holding this lock may still be adding its worker.
		jobs.mu.Lock()
		jobs.mu.Unlock()
		jobs.wg.Wait()
	}()
	s := server(jobs)
	if *httpMode {
		return serveHTTP(ctx, s, *port)
	}
	err := s.Run(ctx, &mcp.StdioTransport{})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
