package helps

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestAcquireAndReleaseScannerBuffer(t *testing.T) {
	buf, release := AcquireScannerBuffer()
	if len(buf) != DefaultInitialScannerBufferSize {
		t.Fatalf("expected buffer length %d, got %d", DefaultInitialScannerBufferSize, len(buf))
	}
	// Write data to verify writable
	copy(buf, []byte("test data"))
	release()
	// Calling release again should be a safe no-op
	release()
}

func TestConfigureScanner(t *testing.T) {
	input := "data: line 1\n\ndata: line 2\n\n"
	scanner := bufio.NewScanner(strings.NewReader(input))
	cleanup := ConfigureScanner(scanner)
	defer cleanup()

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(lines), lines)
	}
}

func TestConfigureScanner_LargeLine(t *testing.T) {
	// Generate a 128 KB single line (larger than initial 64 KB buffer)
	largeLine := "data: " + strings.Repeat("a", 128*1024) + "\n\n"
	scanner := bufio.NewScanner(strings.NewReader(largeLine))
	cleanup := ConfigureScanner(scanner)
	defer cleanup()

	if !scanner.Scan() {
		t.Fatalf("expected scanner to scan large line, err: %v", scanner.Err())
	}
	got := scanner.Text()
	if len(got) != 6+128*1024 {
		t.Fatalf("expected line length %d, got %d", 6+128*1024, len(got))
	}
}

func TestConfigureScanner_NilScanner(t *testing.T) {
	cleanup := ConfigureScanner(nil)
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup function for nil scanner")
	}
	// Safe to call
	cleanup()
}

func BenchmarkConfigureScanner(b *testing.B) {
	data := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		cleanup := ConfigureScanner(scanner)
		for scanner.Scan() {
			_ = scanner.Bytes()
		}
		cleanup()
	}
}
