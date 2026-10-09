// Package app runs one picpull command: start a browser, walk the pages and
// save their images.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/wonhj12/picpull/internal/browser"
	"github.com/wonhj12/picpull/internal/config"
	"github.com/wonhj12/picpull/internal/output"
	"github.com/wonhj12/picpull/internal/page"
	"github.com/wonhj12/picpull/internal/pick"
	"github.com/wonhj12/picpull/internal/store"
)

const (
	nextTimeout    = 10 * time.Second
	statusPolls    = 10
	statusPollWait = 100 * time.Millisecond
)

type Deps struct {
	Printer     *output.Printer
	FindBrowser func() (browser.Browser, error)
	HTTP        *http.Client
	Sleep       func(context.Context, time.Duration)
}

// SleepContext waits for d or until ctx ends.
func SleepContext(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

type runner struct {
	ctx    context.Context
	o      config.Options
	d      Deps
	p      *output.Printer
	s      *page.Session
	total  output.Counts
	failed []string
}

func Run(ctx context.Context, o config.Options, d Deps) int {
	p := d.Printer
	b, err := d.FindBrowser()
	if err != nil {
		p.Error(err.Error())
		return 1
	}
	bctx, cancel, err := browser.Start(ctx, b)
	if err != nil {
		p.Error(err.Error())
		return 1
	}
	defer cancel()
	s, err := page.New(bctx)
	if err != nil {
		p.Error(fmt.Sprintf("could not start Chrome: %v", err))
		return 1
	}
	r := &runner{ctx: ctx, o: o, d: d, p: p, s: s}
	pages, ok := r.walk()
	p.Done(pages, o.Next != "", r.total, r.failed)
	if !ok || len(r.failed) > 0 {
		return 1
	}
	return 0
}

// walk visits pages until a stop condition. It returns how many pages it
// processed and false when it stopped on an error or an interrupt.
func (r *runner) walk() (int, bool) {
	multi := r.o.Next != ""
	visited := map[string]bool{}
	root := r.o.Out

	r.s.Reset()
	status, err := r.s.Open(r.o.URL)
	if r.interrupted() {
		return 0, false
	}
	if err != nil {
		r.p.Stopped("could not open page: "+firstLine(err), true)
		return 0, false
	}
	if status >= 400 {
		r.p.Stopped(fmt.Sprintf("page returned HTTP %d (%s).", status, r.o.URL), true)
		return 0, false
	}

	for index := 1; ; index++ {
		cur, err := r.s.URL()
		if err != nil {
			return index - 1, r.readFailed(err)
		}
		key := withoutFragment(cur)
		if visited[key] {
			r.p.Stopped("returned to a visited page.", false)
			return index - 1, true
		}
		visited[key] = true

		title, _ := r.s.Title()
		host := hostOf(cur)
		if root == "" {
			name := store.SanitizeName(title)
			if name == "" {
				name = host
			}
			root = filepath.Join(r.o.DownloadDir, name)
		}
		label := title
		if strings.TrimSpace(label) == "" {
			label = pathOf(cur)
		}
		r.p.PageStart(index, multi, label, host)
		r.p.PageURL(cur)

		if ok := r.savePage(store.PageDir(root, index, multi), cur); !ok {
			return index, false
		}
		if !multi {
			return index, true
		}
		if r.o.MaxPages > 0 && index >= r.o.MaxPages {
			r.p.Stopped(fmt.Sprintf("reached --max-pages (%d).", r.o.MaxPages), false)
			return index, true
		}
		r.d.Sleep(r.ctx, r.o.Delay)
		if r.interrupted() {
			return index, false
		}
		r.s.Reset()
		res, matches, err := r.s.ClickNext(r.o.Next, nextTimeout)
		if r.interrupted() {
			return index, false
		}
		if err != nil {
			return index, r.readFailed(err)
		}
		if matches > 1 {
			r.p.Warn(fmt.Sprintf("--next matched %d elements; clicked the first one.", matches))
		}
		switch res {
		case page.NextNotFound:
			r.p.Stopped("next button not found.", false)
			return index, true
		case page.NextNoNavigation:
			r.p.Stopped("next button did not navigate.", false)
			return index, true
		}
		if !r.navigatedOK() {
			return index, false
		}
	}
}

// navigatedOK checks the HTTP status of the page --next just opened. The
// response event can trail the load slightly, so an unknown status is polled
// a bounded number of times (responses are recorded without the #fragment); if it stays unknown the page is processed.
func (r *runner) navigatedOK() bool {
	cur, err := r.s.URL()
	if err != nil {
		return r.readFailed(err)
	}
	for i := 0; i < statusPolls; i++ {
		if status, ok := r.s.Status(withoutFragment(cur)); ok {
			if status >= 400 {
				r.p.Stopped(fmt.Sprintf("page returned HTTP %d (%s).", status, cur), true)
				return false
			}
			return true
		}
		r.d.Sleep(r.ctx, statusPollWait)
	}
	return true
}

// savePage collects, filters and saves one page. It returns false on an
// interrupt or a page error.
func (r *runner) savePage(dir, pageURL string) bool {
	if err := r.s.Scroll(); err != nil {
		return r.readFailed(err)
	}
	raws, invalid, err := r.s.Collect(r.o.In)
	if err != nil {
		return r.readFailed(err)
	}
	if invalid {
		r.p.Stopped("invalid --in selector: "+r.o.In, true)
		return false
	}
	if r.o.AllNetwork {
		raws = append(raws, r.s.NetworkImages()...)
	}
	cands := pick.Select(pick.Resolve(raws, pageURL), pick.Filter{
		MinSize: r.o.MinSize, Match: r.o.Match, Scoped: r.o.In != "",
	})
	r.p.Found(len(cands))
	if len(cands) == 0 {
		r.p.NoImages()
	}

	var c output.Counts
	namer := store.NewNamer(r.o.KeepNames)
	defer func() {
		r.p.PageSummary(c)
		r.total.Add(c)
	}()
	for i, cand := range cands {
		if r.ctx.Err() != nil {
			store.RemoveParts(dir)
			r.p.Interrupted()
			return false
		}
		r.saveOne(&c, namer, dir, pageURL, i+1, cand)
		r.p.Progress(i+1, len(cands))
	}
	if r.interrupted() {
		store.RemoveParts(dir)
		return false
	}
	return true
}

func (r *runner) saveOne(c *output.Counts, namer *store.Namer, dir, pageURL string, index int, cand pick.Candidate) {
	stem := namer.Stem(index, cand.URL)
	if store.Exists(dir, stem) {
		c.Skip++
		r.p.Image(output.Skipped, cand.URL, "exists")
		return
	}
	body, ct, cached, err := fetch(r.ctx, r.d, r.s, cand.URL, pageURL)
	if err != nil {
		if r.ctx.Err() != nil {
			return // reported once by the caller
		}
		c.Fail++
		r.failed = append(r.failed, cand.URL)
		r.p.Image(output.Failed, cand.URL, firstLine(err))
		return
	}
	if r.o.MinSize > 0 && (cand.Width == 0 || cand.Height == 0) {
		if w, h, ok := pick.DecodedSize(body); ok && (w < r.o.MinSize || h < r.o.MinSize) {
			c.Skip++
			r.p.Image(output.Skipped, cand.URL, "smaller than --min-size")
			return
		}
	}
	name, err := store.Save(dir, stem, cand.URL, ct, body)
	if err != nil {
		c.Fail++
		r.failed = append(r.failed, cand.URL)
		r.p.Image(output.Failed, cand.URL, "could not write file: "+err.Error())
		return
	}
	c.Success++
	if cached {
		r.p.Image(output.Cached, cand.URL, name)
	} else {
		r.p.Image(output.Downloaded, cand.URL, name)
	}
}

// interrupted reports Ctrl+C once, at the point the walk notices it.
func (r *runner) interrupted() bool {
	if r.ctx.Err() == nil {
		return false
	}
	r.p.Interrupted()
	return true
}

func (r *runner) readFailed(err error) bool {
	if r.interrupted() {
		return false
	}
	r.p.Stopped("could not read page: "+firstLine(err), true)
	return false
}

func firstLine(err error) string {
	if errors.Is(err, context.Canceled) {
		return "interrupted"
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}

func withoutFragment(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	return u.String()
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Hostname()
	}
	return raw
}

func pathOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		return u.Path
	}
	return "/"
}
