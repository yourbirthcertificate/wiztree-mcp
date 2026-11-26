package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestScanResponseText(t *testing.T) {
	ctx := context.Background()
	completed := &job{id: "complete-id", path: `D:\Games`, done: make(chan struct{})}
	completed.final = ScanResult{ID: completed.id, Path: completed.path, State: "complete", Report: &Report{
		Root: Entry{Path: completed.path, Size: 123, Allocated: 4096}, Files: 1,
	}}
	close(completed.done)
	for _, summaryText := range []bool{false, true} {
		st, ct := mcp.NewInMemoryTransports()
		srv := server(&scans{ctx: ctx, jobs: []*job{completed}}, summaryText)
		serverSession, err := srv.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer serverSession.Close()
		client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
		session, err := client.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_scan", Arguments: ScanID{ID: completed.id}})
		if err != nil || res.IsError {
			t.Fatalf("get_scan failed: %v, %v", res, err)
		}
		body := res.Content[0].(*mcp.TextContent).Text
		if json.Valid([]byte(body)) == summaryText || (summaryText && !strings.Contains(body, "4096 allocated bytes")) {
			t.Errorf("summary_text=%v returned %q", summaryText, body)
		}
		structured, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var got ScanResult
		if err := json.Unmarshal(structured, &got); err != nil || got.Report == nil || got.Report.Root.Size != 123 {
			t.Fatalf("structured report missing: %s, %v", structured, err)
		}
	}
}

func TestActiveScanRetry(t *testing.T) {
	ctx := context.Background()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIZTREE_PATH", exe)
	t.Setenv("WIZTREE_ADMIN", "0")
	path := t.TempDir()
	const id = "active-id"
	s := &scans{ctx: ctx, jobs: []*job{{id: id, path: path, rankBy: "allocated", limit: 10, done: make(chan struct{})}}}
	out, err := s.start(ctx, ScanInput{Path: filepath.ToSlash(path) + "/.", RankBy: "allocated"})
	if err != nil || out.ID != id || out.State != "running" {
		t.Fatalf("retry did not reuse active scan: %+v, %v", out, err)
	}
	if _, err := s.start(ctx, ScanInput{Path: path, RankBy: "size"}); err == nil {
		t.Fatal("a different ranking must wait for the active scan")
	}
}

func TestToolAnnotations(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := server(&scans{ctx: ctx}, false)
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	if v := session.InitializeResult().ServerInfo.Version; v != version {
		t.Errorf("server version %q, want %q", v, version)
	}
	want := map[string]struct{ readOnly, idempotent, openWorld bool }{
		"list_drives": {true, true, false},
		"scan":        {true, false, true}, // May start a new job and read a network share.
		"get_scan":    {true, true, false},
		"cancel_scan": {false, true, false},
	}
	seen := 0
	for tl, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		expected, ok := want[tl.Name]
		if !ok {
			t.Errorf("unexpected tool %q", tl.Name)
			continue
		}
		seen++
		a := tl.Annotations
		if a == nil || a.ReadOnlyHint != expected.readOnly || a.IdempotentHint != expected.idempotent ||
			a.OpenWorldHint == nil || *a.OpenWorldHint != expected.openWorld || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s: unexpected annotations %+v", tl.Name, a)
		}
	}
	if seen != len(want) {
		t.Errorf("saw %d tools, want %d", seen, len(want))
	}
}
