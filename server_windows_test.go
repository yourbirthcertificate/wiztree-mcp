package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every tool is local-only and non-destructive; clients rely on these hints.
func TestToolAnnotations(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := server(&scans{ctx: ctx})
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
	want := map[string]bool{"list_drives": true, "scan": true, "get_scan": true, "cancel_scan": false}
	seen := 0
	for tl, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		readOnly, ok := want[tl.Name]
		if !ok {
			t.Errorf("unexpected tool %q", tl.Name)
			continue
		}
		seen++
		a := tl.Annotations
		if a == nil || a.ReadOnlyHint != readOnly || !a.IdempotentHint ||
			a.OpenWorldHint == nil || *a.OpenWorldHint || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s: unexpected annotations %+v", tl.Name, a)
		}
	}
	if seen != len(want) {
		t.Errorf("saw %d tools, want %d", seen, len(want))
	}
}
