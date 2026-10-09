// Package browser finds an installed Google Chrome and starts it
// headless. picpull never downloads a browser itself.
package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/chromedp/chromedp"
)

var ErrNotFound = errors.New("Google Chrome not found. Install Google Chrome and try again.")

type Browser struct {
	Path string
}

type finder struct {
	goos     string
	home     string
	exists   func(string) bool
	lookPath func(string) (string, error)
}

func Find() (Browser, error) {
	home, _ := os.UserHomeDir()
	return finder{
		goos: runtime.GOOS,
		home: home,
		exists: func(p string) bool {
			info, err := os.Stat(p)
			return err == nil && !info.IsDir()
		},
		lookPath: exec.LookPath,
	}.find()
}

func (f finder) find() (Browser, error) {
	if p, ok := f.locate(f.chromePaths(), "google-chrome", "google-chrome-stable"); ok {
		return Browser{Path: p}, nil
	}
	return Browser{}, ErrNotFound
}

func (f finder) locate(paths []string, names ...string) (string, bool) {
	for _, p := range paths {
		if f.exists(p) {
			return p, true
		}
	}
	for _, n := range names {
		if p, err := f.lookPath(n); err == nil {
			return p, true
		}
	}
	return "", false
}

func (f finder) chromePaths() []string {
	if f.goos != "darwin" {
		return nil
	}
	app := "Google Chrome.app/Contents/MacOS/Google Chrome"
	return []string{filepath.Join("/Applications", app), filepath.Join(f.home, "Applications", app)}
}

// Start launches the browser headless and returns a tab context. The
// browser lives until cancel is called or parent ends.
func Start(parent context.Context, b Browser) (context.Context, context.CancelFunc, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(b.Path))
	actx, cancelAlloc := chromedp.NewExecAllocator(parent, opts...)
	ctx, cancelTab := chromedp.NewContext(actx)
	cancel := func() {
		cancelTab()
		cancelAlloc()
	}
	// The first Do starts the browser and binds its life to ctx.
	if err := chromedp.Do(ctx); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("could not start Chrome: %w", err)
	}
	return ctx, cancel, nil
}
