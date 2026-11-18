package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeldoutRunsOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "reports", "heldout.lock"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := study(context.Background(), root, "heldout")
	if err == nil || !strings.Contains(err.Error(), "run once") {
		t.Fatalf("a second held-out run must be refused, got %v", err)
	}
	if err := study(context.Background(), root, "prod"); err == nil {
		t.Fatal("unknown world accepted")
	}
}
