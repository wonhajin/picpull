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

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/wonhj12/picpull/internal/pick"
)

type NextResult int

const (
	NextNotFound NextResult = iota
	NextNoNavigation
	NextNavigated
)

type response struct {
	id     network.RequestID
	status int64
	mime   string
}

type Session struct {
	ctx context.Context

	mu     sync.Mutex
	urlIDs map[string][]network.RequestID // request ids seen under each URL, oldest first
	resp   map[network.RequestID]response
	images []string // image response URLs in arrival order
	seen   map[string]bool
}

func New(ctx context.Context) (*Session, error) {
	s := &Session{ctx: ctx}
	s.Reset()
	if _, err := chromedp.Call(ctx, page.AddScriptToEvaluateOnNewDocument,
		page.AddScriptToEvaluateOnNewDocumentParams{Source: hookJS}); err != nil {
		return nil, err
	}
	// Subscribe before any navigation; the iterators buffer events.
	requests := chromedp.Events(ctx, network.RequestWillBeSent)
	responses := chromedp.Events(ctx, network.ResponseReceived)
	go func() {
		for ev, err := range requests {
			if err != nil {
				if s.ctx.Err() != nil {
					return
				}
				continue
			}
			if ev.Request == nil {
				continue
			}
			s.recordRequest(ev.RequestID, ev.Request.URL)
		}
	}()
	go func() {
		for ev, err := range responses {
			if err != nil {
				if s.ctx.Err() != nil {
					return
				}
				continue
			}
			s.record(ev)
		}
	}()
	return s, nil
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
	s.resp[ev.RequestID] = response{id: ev.RequestID, status: ev.Response.Status, mime: ev.Response.MimeType}
	s.addID(ev.Response.URL, ev.RequestID)
	if ev.Type == network.ResourceTypeImage && ok2xx(ev.Response.Status) && !s.seen[ev.Response.URL] {
		s.seen[ev.Response.URL] = true
		s.images = append(s.images, ev.Response.URL)
	}
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
// bodies on navigation anyway.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.urlIDs = map[string][]network.RequestID{}
	s.resp = map[network.RequestID]response{}
	s.images = nil
	s.seen = map[string]bool{}
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
	r, err := chromedp.Run(s.ctx, chromedp.Evaluate[result](call(fetchJS, url), chromedp.EvalAwaitPromise))
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
