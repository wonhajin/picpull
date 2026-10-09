// Package page drives one browser tab: open, scroll, collect image
// candidates, reuse the bytes the browser already received, and follow the
// next button.
package page

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/performance"
	"github.com/chromedp/chromedp"

	"github.com/wonhj12/picpull/internal/pick"
)

const eventErrorWait = 100 * time.Millisecond

// fetchTimeout bounds one in-page fetch; a var so tests can shorten it.
var fetchTimeout = 30 * time.Second

type NextResult int

const (
	NextNotFound NextResult = iota
	NextNoNavigation
	NextNavigated
)

type response struct {
	id     network.RequestID
	url    string
	status int64
	mime   string
}

type Session struct {
	ctx context.Context

	mu      sync.Mutex
	urlIDs  map[string][]network.RequestID // request ids seen under each URL, oldest first
	resp    map[network.RequestID]response
	images  []string // image response URLs in arrival order
	seen    map[string]bool
	imageBy map[string]network.RequestID // request that first reported each image URL

	cutoff float64 // browser clock at the last Reset; 0 when unknown

	reqs  map[network.RequestID]bool // requests seen since Reset
	stale map[network.RequestID]bool // requests of the page before the last Reset
}

func New(ctx context.Context) (*Session, error) {
	s := &Session{ctx: ctx}
	if _, err := chromedp.Call(ctx, page.AddScriptToEvaluateOnNewDocument,
		page.AddScriptToEvaluateOnNewDocumentParams{Source: hookJS}); err != nil {
		return nil, err
	}
	// getMetrics, which Reset reads the browser clock from, is empty until
	// the domain is enabled.
	if _, err := chromedp.Call(ctx, performance.Enable, performance.EnableParams{}); err != nil {
		return nil, err
	}
	s.Reset()
	// Subscribe before any navigation; the iterators buffer events.
	requests := chromedp.Events(ctx, network.RequestWillBeSent)
	responses := chromedp.Events(ctx, network.ResponseReceived)
	go func() {
		for ev, err := range requests {
			if err != nil {
				if !s.backoff() {
					return
				}
				continue
			}
			if ev.Request == nil {
				continue
			}
			if s.startedBeforeReset(ev.RequestID, ev.Timestamp.Float64()) {
				continue
			}
			s.recordRequest(ev.RequestID, ev.Request.URL)
		}
	}()
	go func() {
		for ev, err := range responses {
			if err != nil {
				if !s.backoff() {
					return
				}
				continue
			}
			s.record(ev)
		}
	}()
	return s, nil
}

// backoff pauses after an event error so a failing iterator cannot spin. It
// returns false once the session is over.
func (s *Session) backoff() bool {
	select {
	case <-s.ctx.Done():
		return false
	case <-time.After(eventErrorWait):
		return true
	}
}

// addID files id under url once.
func (s *Session) addID(url string, id network.RequestID) {
	if slices.Contains(s.urlIDs[url], id) {
		return
	}
	s.urlIDs[url] = append(s.urlIDs[url], id)
}

// recordRequest fires once per redirect hop with the same id.
func (s *Session) recordRequest(id network.RequestID, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stale[id] {
		return
	}
	s.reqs[id] = true
	s.addID(url, id)
}

// record stores the final response. Redirect hops are resolved at lookup
// time, so the request and response events may arrive in either order.
func (s *Session) record(ev network.EventResponseReceived) {
	if ev.Response == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A late response to a request made before Reset belongs to no one.
	if s.stale[ev.RequestID] {
		return
	}
	s.reqs[ev.RequestID] = true
	s.resp[ev.RequestID] = response{id: ev.RequestID, url: ev.Response.URL, status: ev.Response.Status, mime: ev.Response.MimeType}
	s.addID(ev.Response.URL, ev.RequestID)
	if ev.Type == network.ResourceTypeImage && ok2xx(ev.Response.Status) && !s.seen[ev.Response.URL] {
		s.seen[ev.Response.URL] = true
		s.images = append(s.images, ev.Response.URL)
		s.imageBy[ev.Response.URL] = ev.RequestID
	}
}

// startedBeforeReset marks the request stale when the browser started it
// before the last Reset, even if its event is processed only now.
func (s *Session) startedBeforeReset(id network.RequestID, started float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.beforeReset(started) {
		return false
	}
	s.markStale(id)
	s.purge(id)
	return true
}

// purge forgets what a response that beat its request event already
// recorded for id.
func (s *Session) purge(id network.RequestID) {
	r, ok := s.resp[id]
	delete(s.resp, id)
	delete(s.reqs, id)
	for url, ids := range s.urlIDs {
		s.urlIDs[url] = slices.DeleteFunc(ids, func(x network.RequestID) bool { return x == id })
	}
	if ok && s.imageBy[r.url] == id {
		delete(s.imageBy, r.url)
		delete(s.seen, r.url)
		s.images = slices.DeleteFunc(s.images, func(u string) bool { return u == r.url })
	}
}

// beforeReset compares a browser timestamp with the clock read at Reset.
func (s *Session) beforeReset(t float64) bool { return t > 0 && t < s.cutoff }

func (s *Session) markStale(id network.RequestID) {
	if s.stale == nil {
		s.stale = map[network.RequestID]bool{}
	}
	s.stale[id] = true
}

// browserNow reads the browser's monotonic clock, the one network events are
// stamped with. It returns 0 when that is unavailable.
func (s *Session) browserNow() float64 {
	if s.ctx == nil {
		return 0
	}
	res, err := chromedp.Call(s.ctx, performance.GetMetrics, cdp.Empty{})
	if err != nil {
		return 0
	}
	for _, m := range res.Metrics {
		if m.Name == "Timestamp" {
			return m.Value
		}
	}
	return 0
}

// lookup picks the most recent 2xx response for url, else the most recent
// response: a failed response must not hide a good one.
func (s *Session) lookup(url string) (response, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var last response
	found := false
	ids := s.urlIDs[url]
	for i := len(ids) - 1; i >= 0; i-- {
		r, ok := s.resp[ids[i]]
		if !ok {
			continue
		}
		if ok2xx(r.status) {
			return r, true
		}
		if !found {
			last, found = r, true
		}
	}
	return last, found
}

func ok2xx(status int64) bool { return status >= 200 && status < 300 }

// Reset forgets the previous page's responses; the browser drops their
// bodies on navigation anyway. Responses to requests the browser started
// before Reset are ignored, even when their events are processed later.
func (s *Session) Reset() {
	cutoff := s.browserNow()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutoff = cutoff
	s.stale = s.reqs
	s.reqs = map[network.RequestID]bool{}
	s.urlIDs = map[string][]network.RequestID{}
	s.resp = map[network.RequestID]response{}
	s.images = nil
	s.seen = map[string]bool{}
	s.imageBy = map[string]network.RequestID{}
}

func (s *Session) Open(url string) (int64, error) {
	resp, err := chromedp.Run(s.ctx, chromedp.NavigateResponse(url))
	if err != nil {
		return 0, err
	}
	if resp == nil {
		return 0, errors.New("no response")
	}
	return resp.Status, nil
}

func (s *Session) Scroll() error {
	_, err := chromedp.Run(s.ctx, chromedp.Evaluate[bool](scrollJS, chromedp.EvalAwaitPromise))
	return err
}

func (s *Session) Title() (string, error) { return chromedp.Run(s.ctx, chromedp.Title()) }

func (s *Session) URL() (string, error) { return chromedp.Run(s.ctx, chromedp.Location()) }

func call(fn, arg string) string {
	b, _ := json.Marshal(arg)
	return fmt.Sprintf("(%s)(%s)", fn, b)
}

// ValidSelector reports whether sel parses as a CSS selector.
func (s *Session) ValidSelector(sel string) (bool, error) {
	return chromedp.Run(s.ctx, chromedp.Evaluate[bool](call(validSelectorJS, sel)))
}

func (s *Session) Collect(in string) ([]pick.Raw, bool, error) {
	type result struct {
		Invalid bool       `json:"invalid"`
		Items   []pick.Raw `json:"items"`
	}
	r, err := chromedp.Run(s.ctx, chromedp.Evaluate[result](call(collectJS, in)))
	if err != nil {
		return nil, false, err
	}
	return r.Items, r.Invalid, nil
}

func (s *Session) NetworkImages() []pick.Raw {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]pick.Raw, len(s.images))
	for i, u := range s.images {
		out[i] = pick.Raw{Kind: pick.KindNetwork, Src: u}
	}
	return out
}

// Captured returns the bytes the browser already received for url. Some
// sites refuse requests from outside the browser, so this comes first.
func (s *Session) Captured(url string) ([]byte, string, bool) {
	r, ok := s.lookup(url)
	if !ok || !ok2xx(r.status) {
		return nil, "", false
	}
	res, err := chromedp.Call(s.ctx, network.GetResponseBody, network.GetResponseBodyParams{RequestID: r.id})
	if err != nil || len(res.Body) == 0 {
		return nil, "", false
	}
	return res.Body, r.mime, true
}

func (s *Session) FetchInPage(url string) ([]byte, string, error) {
	type result struct {
		Status int    `json:"status"`
		Type   string `json:"type"`
		Data   string `json:"data"`
	}
	ctx, cancel := context.WithTimeout(s.ctx, fetchTimeout)
	defer cancel()
	arg, _ := json.Marshal(url)
	expr := fmt.Sprintf("(%s)(%s, %d)", fetchJS, arg, fetchTimeout.Milliseconds())
	r, err := chromedp.Run(ctx, chromedp.Evaluate[result](expr, chromedp.EvalAwaitPromise))
	if err != nil {
		return nil, "", err
	}
	if r.Status < 200 || r.Status >= 300 {
		return nil, "", fmt.Errorf("HTTP %d", r.Status)
	}
	_, b64, found := strings.Cut(r.Data, ",")
	if !found {
		return nil, "", errors.New("empty response")
	}
	body, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 {
		return nil, "", errors.New("empty response")
	}
	return body, r.Type, nil
}

func (s *Session) UserAgent() (string, error) {
	return chromedp.Run(s.ctx, chromedp.Evaluate[string](`navigator.userAgent`))
}

func (s *Session) CookieHeader(url string) (string, error) {
	res, err := chromedp.Call(s.ctx, network.GetCookies, network.GetCookiesParams{URLs: []string{url}})
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(res.Cookies))
	for _, c := range res.Cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; "), nil
}

// ClickNext clicks the first element matching target and waits up to
// timeout for the address to change and the new page to finish loading.
// The int result is how many elements matched.
func (s *Session) ClickNext(target string, timeout time.Duration) (NextResult, int, error) {
	n, err := chromedp.Run(s.ctx, chromedp.Evaluate[int](call(markNextJS, target)))
	if err != nil {
		return NextNotFound, 0, err
	}
	if n == 0 {
		return NextNotFound, 0, nil
	}
	before, err := s.URL()
	if err != nil {
		return NextNotFound, n, err
	}
	// A JS click fires onclick handlers such as location.href='...' even when
	// the element is off screen.
	if _, err := chromedp.Run(s.ctx, chromedp.Evaluate[bool](
		`(document.querySelector('[data-picpull-next]').click(), true)`)); err != nil {
		return NextNotFound, n, err
	}
	deadline := time.Now().Add(timeout)
	navigated := false
	for time.Now().Before(deadline) {
		if err := s.ctx.Err(); err != nil {
			return NextNoNavigation, n, err
		}
		time.Sleep(200 * time.Millisecond)
		// Errors are expected while the old document is torn down.
		now, err := s.URL()
		if err != nil || now == before {
			continue
		}
		navigated = true
		// A heavy page may not finish loading in time; the URL change alone
		// proves the click navigated.
		ready, err := chromedp.Run(s.ctx, chromedp.Evaluate[string](`document.readyState`))
		if err == nil && ready == "complete" {
			break
		}
	}
	if navigated {
		return NextNavigated, n, nil
	}
	return NextNoNavigation, n, nil
}

// Status returns the recorded response status for url, as seen on the wire.
func (s *Session) Status(url string) (int64, bool) {
	r, ok := s.lookup(url)
	return r.status, ok
}
