// Package testbrowser starts a real browser for integration tests.
package testbrowser

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/wonhj12/picpull/internal/browser"
)

// Find skips the test when no browser is available. PICPULL_TEST_BROWSER
// points at an executable for machines without Chrome or Chromium installed.
func Find(t *testing.T) browser.Browser {
	t.Helper()
	b := browser.Browser{Name: "Chrome", Path: os.Getenv("PICPULL_TEST_BROWSER")}
	if b.Path == "" {
		found, _, err := browser.Find("")
		if err != nil {
			t.Skip("no browser: set PICPULL_TEST_BROWSER to run browser tests")
		}
		b = found
	}
	return b
}

// Start launches the browser from Find and returns its tab context.
func Start(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel, err := browser.Start(context.Background(), Find(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	return ctx
}

// PNG returns an encoded blank PNG of the given size.
func PNG(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}
