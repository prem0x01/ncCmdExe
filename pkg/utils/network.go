package utils

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/kbinani/screenshot"
)

func TakeScreenShot(dir string) ([]string, error) {
	if dir == "" {
		dir = os.TempDir()
	}

	n := screenshot.NumActiveDisplays()
	if n <= 0 {
		return nil, fmt.Errorf("no active displays found (headless host or no display server?)")
	}

	var paths []string
	for i := 0; i < n; i++ {
		bounds := screenshot.GetDisplayBounds(i)
		img, err := screenshot.CaptureRect(bounds)
		if err != nil {
			return paths, fmt.Errorf("capture display %d: %w", i, err)
		}

		path := filepath.Join(dir, fmt.Sprintf("screenshot-%d.png", i))
		f, err := os.Create(path)
		if err != nil {
			return paths, fmt.Errorf("create %s: %w", path, err)
		}

		if err := png.Encode(f, img); err != nil {
			f.Close()
			return paths, fmt.Errorf("encode %s: %w", path, err)
		}
		f.Close()

		paths = append(paths, path)
	}

	return paths, nil
}
