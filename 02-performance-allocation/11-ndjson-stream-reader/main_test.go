package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: write content to a temp file and return an *os.File opened for reading.
func createTempNDJSON(t *testing.T, content string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open temp file: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// Self-Correction 1: "If a 200KB line crashes with 'token too long': you failed."
func TestLargeLine(t *testing.T) {
	t.Run("200KB line must not crash", func(t *testing.T) {
		largeLine := strings.Repeat("A", 200*1024)
		f := createTempNDJSON(t, largeLine)

		reader := NewReader()
		var got []byte

		err := reader.ReadNDJSON(context.Background(), f, func(line []byte) error {
			got = make([]byte, len(line))
			copy(got, line)
			return nil
		})

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(got) != 200*1024 {
			t.Fatalf("expected line length %d, got %d", 200*1024, len(got))
		}
	})
}

// Self-Correction 2: "If cancellation doesn't stop promptly: you failed."
func TestCancellation(t *testing.T) {
	t.Run("context cancellation stops processing", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 1000; i++ {
			sb.WriteString(`{"id":"` + strings.Repeat("x", 100) + "\"}\n")
		}
		f := createTempNDJSON(t, sb.String())

		ctx, cancel := context.WithCancel(context.Background())
		reader := NewReader()
		linesProcessed := 0

		err := reader.ReadNDJSON(ctx, f, func(line []byte) error {
			linesProcessed++
			if linesProcessed == 2 {
				cancel()
			}
			return nil
		})

		if err == nil {
			t.Fatal("expected cancellation error, got nil")
		}
		if linesProcessed > 3 {
			t.Fatalf("expected prompt stop after cancel, but processed %d lines", linesProcessed)
		}
	})
}

// Self-Correction 3: "If you allocate a new buffer each line: you failed the low-allocation goal."
func TestLowAllocation(t *testing.T) {
	t.Run("low allocations per line", func(t *testing.T) {
		content := "{\"a\":1}\n{\"b\":2}\n{\"c\":3}\n{\"d\":4}\n{\"e\":5}\n"
		path := filepath.Join(t.TempDir(), "test.ndjson")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write temp file: %v", err)
		}

		reader := NewReader()

		result := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				f, err := os.Open(path)
				if err != nil {
					b.Fatal(err)
				}
				_ = reader.ReadNDJSON(context.Background(), f, func(line []byte) error {
					return nil
				})
				f.Close()
			}
		})

		allocsPerOp := result.AllocsPerOp()
		t.Logf("allocs/op: %d", allocsPerOp)
		if allocsPerOp > 15 {
			t.Fatalf("too many allocations: %d per op, expected low-allocation behavior", allocsPerOp)
		}
	})
}
