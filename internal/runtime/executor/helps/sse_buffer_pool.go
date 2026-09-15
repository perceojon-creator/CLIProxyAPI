package helps

import (
	"bufio"
	"sync"
)

const (
	// DefaultInitialScannerBufferSize is the initial capacity of pooled SSE scanner buffers (64 KB).
	DefaultInitialScannerBufferSize = 64 * 1024

	// DefaultMaxScannerBufferSize is the upper limit for SSE scanner tokens (50 MB).
	DefaultMaxScannerBufferSize = 52_428_800

	// maxPooledBufferCapacity is the upper bound on buffer capacity retained in the sync.Pool (1 MB).
	// Buffers that grew larger than this during heavy reasoning/multimodal bursts are discarded
	// to avoid pinning excess memory.
	maxPooledBufferCapacity = 1024 * 1024
)

var sseScannerBufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, DefaultInitialScannerBufferSize)
		return &b
	},
}

// AcquireScannerBuffer obtains a reusable byte slice from the pool.
// It returns the buffer and a release function that safely returns it to the pool upon completion.
func AcquireScannerBuffer() ([]byte, func()) {
	ptr, ok := sseScannerBufferPool.Get().(*[]byte)
	if !ok || ptr == nil || len(*ptr) < DefaultInitialScannerBufferSize {
		b := make([]byte, DefaultInitialScannerBufferSize)
		ptr = &b
	}
	buf := *ptr

	release := func() {
		if ptr == nil {
			return
		}
		// Only recycle buffers within reasonable size to prevent unbounded memory growth.
		if cap(*ptr) <= maxPooledBufferCapacity {
			// Reset length to initial size
			*ptr = (*ptr)[:DefaultInitialScannerBufferSize]
			sseScannerBufferPool.Put(ptr)
		}
		ptr = nil
	}

	return buf, release
}

// ConfigureScanner sets up a bufio.Scanner with a pooled initial buffer and returns
// a cleanup function that must be deferred by the caller to return the buffer to the pool.
func ConfigureScanner(scanner *bufio.Scanner) func() {
	return ConfigureScannerWithSize(scanner, DefaultMaxScannerBufferSize)
}

// ConfigureScannerWithSize sets up a bufio.Scanner with a pooled initial buffer and custom max size.
func ConfigureScannerWithSize(scanner *bufio.Scanner, maxBufferSize int) func() {
	if scanner == nil {
		return func() {}
	}
	if maxBufferSize <= 0 {
		maxBufferSize = DefaultMaxScannerBufferSize
	}
	buf, release := AcquireScannerBuffer()
	scanner.Buffer(buf, maxBufferSize)
	return release
}
