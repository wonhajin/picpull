package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/wonhj12/picpull/internal/browser"
	"github.com/wonhj12/picpull/internal/config"
	"github.com/wonhj12/picpull/internal/output"
	"github.com/wonhj12/picpull/internal/page"
	"github.com/wonhj12/picpull/internal/testbrowser"
)

type harness struct {
	out, errw bytes.Buffer
	deps      Deps
}

func newHarness(t *testing.T, level output.Level) *harness {
	b := testbrowser.Find(t)
	h := &harness{}
	h.deps = Deps{
		Printer:     output.New(&h.out, &h.errw, level, false, false, 0),
		FindBrowser: func() (browser.Browser, error) { return b, nil },
		HTTP:        &http.Client{Timeout: 5 * time.Second},
		Sleep:       func(context.Context, time.Duration) {},
	}
	return h
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func site(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	page := func(path, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, body)
		})
	}
	page("/ep/1", `<html><head><title>Ep: 1/2</title></head><body style="margin:0">
<img src="/img/b.png" style="display:block;width:30px;height:40px;position:absolute;top:0;left:100px">
<img src="/img/a.png" style="display:block;width:30px;height:40px">
<img src="/icon.png" style="width:8px;height:8px">
<a class="next" href="/ep/2">Next</a></body></html>`)
	page("/ep/2", `<html><head><title>Ep 2</title></head><body>
<img src="/img/c.png"><img src="/missing.png"></body></html>`)
	page("/untitled", `<html><head><title>   </title></head><body><img src="/img/a.png"></body></html>`)
	page("/ep/dead", `<html><head><title>Dead</title></head><body>
<img src="/img/a.png"><a class="next" href="/gone">Next</a></body></html>`)
	page("/ep/dead-frag", `<html><head><title>Dead</title></head><body>
<img src="/img/a.png"><a class="next" href="/gone#top">Next</a></body></html>`)
	page("/ep/softhtml", `<html><head><title>Soft</title></head><body><img src="/softhtml.png"></body></html>`)
	page("/ep/softnoct", `<html><head><title>Soft</title></head><body><img src="/softnoct.png"></body></html>`)
	page("/empty", `<html><head><title>Empty</title></head><body>no images</body></html>`)
	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(testbrowser.PNG(300, 400))
	})
	mux.HandleFunc("/icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(testbrowser.PNG(8, 8))
	})
	mux.HandleFunc("/softhtml.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body>please log in</body></html>")
	})
	mux.HandleFunc("/softnoct.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header()["Content-Type"] = nil // stop net/http from sniffing one
		fmt.Fprint(w, "<!DOCTYPE html><html><body>please log in</body></html>")
	})
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})
	mux.HandleFunc("/forbidden", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestSinglePageInScreenOrder(t *testing.T) {
	ts := site(t)
	h := newHarness(t, output.Verbose)
	root := t.TempDir()
	code := Run(context.Background(), config.Options{URL: ts.URL + "/ep/1", DownloadDir: root, Level: output.Verbose}, h.deps)
	if code != 0 {
		t.Fatalf("code = %d\n%s\n%s", code, h.out.String(), h.errw.String())
	}
	// The folder comes from the title "Ep: 1/2".
	got := files(t, filepath.Join(root, "Ep 1 2"))
	if strings.Join(got, ",") != "001.png,002.png,003.png" {
		t.Fatalf("files = %v", got)
	}
	// b.png comes first in the HTML but sits right of a.png on the same row;
	// the icon is on the next line. All three were loaded by the browser.
	for _, want := range []string{
		"cached      " + ts.URL + "/img/a.png → 001.png",
		"cached      " + ts.URL + "/img/b.png → 002.png",
		"cached      " + ts.URL + "/icon.png → 003.png",
		"Done · 3 success · 0 skip · 0 fail",
	} {
		if !strings.Contains(h.out.String(), want) {
			t.Fatalf("missing %q in stdout:\n%s", want, h.out.String())
		}
	}
}

func TestMinSizeAndRerunSkips(t *testing.T) {
	ts := site(t)
	root := t.TempDir()
	o := config.Options{URL: ts.URL + "/ep/1", DownloadDir: root, MinSize: 100, Level: output.Normal}
	h := newHarness(t, output.Normal)
	if code := Run(context.Background(), o, h.deps); code != 0 {
		t.Fatalf("code = %d %s", code, h.errw.String())
	}
	dir := filepath.Join(root, "Ep 1 2")
	if got := files(t, dir); strings.Join(got, ",") != "001.png,002.png" {
		t.Fatalf("files = %v", got)
	}
	h2 := newHarness(t, output.Normal)
	Run(context.Background(), o, h2.deps)
	if !strings.Contains(h2.out.String(), "Done · 0 success · 2 skip · 0 fail") {
		t.Fatalf("rerun stdout = %s", h2.out.String())
	}

	// A leftover 001.png.part is an interrupted download: it must not count
	// as the finished 001.png, so that image is fetched again.
	os.Remove(filepath.Join(dir, "001.png"))
	os.WriteFile(filepath.Join(dir, "001.png.part"), []byte("x"), 0o644)
	h3 := newHarness(t, output.Normal)
	Run(context.Background(), o, h3.deps)
	if !strings.Contains(h3.out.String(), "Done · 1 success · 1 skip · 0 fail") {
		t.Fatalf("stale .part stdout = %s", h3.out.String())
	}
	if got := files(t, dir); strings.Join(got, ",") != "001.png,002.png" {
		t.Fatalf("files after stale .part = %v", got)
	}
}

func TestUntitledPageUsesHostFolder(t *testing.T) {
	ts := site(t)
	h := newHarness(t, output.Normal)
	root := t.TempDir()
	code := Run(context.Background(), config.Options{URL: ts.URL + "/untitled", DownloadDir: root, Level: output.Normal}, h.deps)
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, h.errw.String())
	}
	if got := files(t, filepath.Join(root, "127.0.0.1")); strings.Join(got, ",") != "001.png" {
		t.Fatalf("files = %v", got)
	}
}

func TestNextPageHTTPErrorStops(t *testing.T) {
	ts := site(t)
	for _, tc := range []struct{ name, start, stopURL string }{
		{"plain", "/ep/dead", "/gone"},
		{"fragment", "/ep/dead-frag", "/gone#top"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, output.Normal)
			o := config.Options{URL: ts.URL + tc.start, Out: t.TempDir(), Next: "a.next", Level: output.Normal}
			code := Run(context.Background(), o, h.deps)
			if code != 1 || !strings.Contains(h.errw.String(), "Stopped: page returned HTTP 404 ("+ts.URL+tc.stopURL+").") {
				t.Fatalf("code = %d stderr = %s", code, h.errw.String())
			}
			if strings.Contains(h.out.String(), "▸ [002]") {
				t.Fatalf("404 page was processed:\n%s", h.out.String())
			}
		})
	}
}

func TestFollowNextWithOutAndFailures(t *testing.T) {
	ts := site(t)
	out := t.TempDir()
	h := newHarness(t, output.Normal)
	o := config.Options{URL: ts.URL + "/ep/1", Out: out, Next: "a.next", Level: output.Normal}
	code := Run(context.Background(), o, h.deps)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (missing.png fails)", code)
	}
	got := strings.Join(files(t, out), ",")
	if got != "001/001.png,001/002.png,001/003.png,002/001.png" {
		t.Fatalf("files = %s", got)
	}
	if !strings.Contains(h.out.String(), "Stopped: next button not found.") ||
		!strings.Contains(h.out.String(), "Done · 2 pages · 4 success · 0 skip · 1 fail") ||
		!strings.Contains(h.errw.String(), "Failed:\n  "+ts.URL+"/missing.png") {
		t.Fatalf("stdout = %s\nstderr = %s", h.out.String(), h.errw.String())
	}
}

func TestHTMLResponseIsNotAnImage(t *testing.T) {
	for _, name := range []string{"softhtml", "softnoct"} {
		t.Run(name, func(t *testing.T) {
			ts := site(t)
			out := t.TempDir()
			h := newHarness(t, output.Verbose)
			code := Run(context.Background(), config.Options{URL: ts.URL + "/ep/" + name, Out: out, Level: output.Verbose}, h.deps)
			if code != 1 {
				t.Fatalf("code = %d, want 1", code)
			}
			if got := files(t, out); len(got) != 0 {
				t.Fatalf("saved %v, want nothing", got)
			}
			if !strings.Contains(h.errw.String(), "failed      "+ts.URL+"/"+name+".png: not an image (text/html)") ||
				!strings.Contains(h.out.String(), "Done · 0 success · 0 skip · 1 fail") ||
				!strings.Contains(h.errw.String(), "Failed:\n  "+ts.URL+"/"+name+".png") {
				t.Fatalf("stdout = %s\nstderr = %s", h.out.String(), h.errw.String())
			}
		})
	}
}

func TestMaxPages(t *testing.T) {
	ts := site(t)
	h := newHarness(t, output.Normal)
	o := config.Options{URL: ts.URL + "/ep/1", Out: t.TempDir(), Next: "Next", MaxPages: 1, Level: output.Normal}
	if code := Run(context.Background(), o, h.deps); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(h.out.String(), "Stopped: reached --max-pages (1).") {
		t.Fatalf("stdout = %s", h.out.String())
	}
}

func TestNoImagesIsAWarningNotAFailure(t *testing.T) {
	ts := site(t)
	h := newHarness(t, output.Normal)
	code := Run(context.Background(), config.Options{URL: ts.URL + "/empty", Out: t.TempDir(), Level: output.Normal}, h.deps)
	if code != 0 || !strings.Contains(h.errw.String(), "No images found.") {
		t.Fatalf("code = %d stderr = %s", code, h.errw.String())
	}
}

func TestHTTPErrorStops(t *testing.T) {
	ts := site(t)
	h := newHarness(t, output.Normal)
	code := Run(context.Background(), config.Options{URL: ts.URL + "/forbidden", Out: t.TempDir(), Level: output.Normal}, h.deps)
	if code != 1 || !strings.Contains(h.errw.String(), "Stopped: page returned HTTP 403 ("+ts.URL+"/forbidden).") {
		t.Fatalf("code = %d stderr = %s", code, h.errw.String())
	}
}

func TestBrowserNotFound(t *testing.T) {
	var out, errw bytes.Buffer
	d := Deps{
		Printer:     output.New(&out, &errw, output.Normal, false, false, 0),
		FindBrowser: func() (browser.Browser, error) { return browser.Browser{}, browser.ErrNotFound },
	}
	code := Run(context.Background(), config.Options{URL: "https://e.com"}, d)
	want := "Error: Google Chrome not found. Install Google Chrome and try again.\n"
	if code != 1 || errw.String() != want {
		t.Fatalf("code = %d stderr = %q", code, errw.String())
	}
}

// cancelOnWrite cancels the run the moment a matching line is printed, so
// the interrupt lands at a known point instead of depending on timing.
type cancelOnWrite struct {
	w      io.Writer
	match  string
	cancel context.CancelFunc
}

func (c cancelOnWrite) Write(p []byte) (int, error) {
	if strings.Contains(string(p), c.match) {
		c.cancel()
	}
	return c.w.Write(p)
}

func TestInterruptRemovesPartsAndReports(t *testing.T) {
	ts := site(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHarness(t, output.Normal)
	h.deps.Printer = output.New(cancelOnWrite{&h.out, "Found ", cancel}, &h.errw, output.Normal, false, false, 0)
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "009.png.part"), []byte("x"), 0o644)

	code := Run(ctx, config.Options{URL: ts.URL + "/ep/1", Out: out, Level: output.Normal}, h.deps)
	if code != 1 {
		t.Fatalf("code = %d", code)
	}
	if got := files(t, out); len(got) != 0 {
		t.Fatalf("files = %v, want none (.part removed, nothing saved)", got)
	}
	if strings.Count(h.errw.String(), "Interrupted.") != 1 ||
		!strings.Contains(h.out.String(), "Done · 0 success · 0 skip · 0 fail") {
		t.Fatalf("stdout = %s\nstderr = %s", h.out.String(), h.errw.String())
	}
}

// blankRunner returns a runner whose tab sits on about:blank, a page with no
// recorded response, so --next's status check stays unconfirmed.
func blankRunner(t *testing.T, ctx context.Context, h *harness) *runner {
	t.Helper()
	bctx, cancel, err := browser.Start(ctx, testbrowser.Find(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	s, err := page.New(bctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chromedp.Run(bctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatal(err)
	}
	return &runner{ctx: ctx, d: h.deps, p: h.deps.Printer, s: s}
}

func TestNavigatedOKPassesWhenStatusStaysUnknown(t *testing.T) {
	h := newHarness(t, output.Normal)
	sleeps := 0
	h.deps.Sleep = func(context.Context, time.Duration) { sleeps++ }
	r := blankRunner(t, context.Background(), h)
	if !r.navigatedOK() {
		t.Fatalf("navigatedOK = false, want the page processed\n%s", h.errw.String())
	}
	if sleeps != statusPolls {
		t.Fatalf("polled %d times, want %d", sleeps, statusPolls)
	}
}

func TestNavigatedOKStopsWhenInterruptedWhilePolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHarness(t, output.Normal)
	sleeps := 0
	h.deps.Sleep = func(context.Context, time.Duration) { sleeps++; cancel() }
	r := blankRunner(t, ctx, h)
	if r.navigatedOK() {
		t.Fatal("navigatedOK = true after Ctrl+C")
	}
	if sleeps != 1 || strings.Count(h.errw.String(), "Interrupted.") != 1 {
		t.Fatalf("polled %d times, stderr = %q", sleeps, h.errw.String())
	}
}

func TestInterruptWhileStartingBrowser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := newHarness(t, output.Normal)
	code := Run(ctx, config.Options{URL: "http://127.0.0.1/", Out: t.TempDir(), Level: output.Normal}, h.deps)
	if code != 1 || h.errw.String() != "Interrupted.\n" {
		t.Fatalf("code = %d stderr = %q", code, h.errw.String())
	}
}

func TestInvalidInSelectorStops(t *testing.T) {
	ts := site(t)
	h := newHarness(t, output.Normal)
	o := config.Options{URL: ts.URL + "/ep/1", Out: t.TempDir(), In: "#(", Level: output.Normal}
	code := Run(context.Background(), o, h.deps)
	if code != 1 || !strings.Contains(h.errw.String(), "Stopped: invalid --in selector: #(") ||
		strings.Contains(h.out.String(), "Found ") {
		t.Fatalf("code = %d stdout = %s stderr = %s", code, h.out.String(), h.errw.String())
	}
}
