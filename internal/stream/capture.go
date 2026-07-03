// Package stream captures the primary display as JPEG-encoded bytes.
package stream

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"sync"

	"github.com/kbinani/screenshot"
)

// Quality is the JPEG quality used when encoding frames (1-100).
// Lower values produce smaller frames (less bandwidth) at the cost of detail.
// 60 is a good balance for screen streaming.
var Quality = 60

// bufPool recycles encode buffers to avoid per-frame heap allocations.
var bufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// CaptureJPEG grabs the primary display (display index 0) and returns a JPEG-
// encoded snapshot.  The caller owns the returned slice.
func CaptureJPEG() ([]byte, error) {
	n := screenshot.NumActiveDisplays()
	if n == 0 {
		return nil, fmt.Errorf("no active displays found (headless?)")
	}

	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}

	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)

	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: Quality}); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	// Return a copy so the caller is independent of the pooled buffer.
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	return out, nil
}
