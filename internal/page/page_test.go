package page

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"

	"github.com/wonhajin/picpull/internal/pick"
	"github.com/wonhajin/picpull/internal/testbrowser"
)

func server(t *testing.T, pages map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range pages {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Set-Cookie", "sid=abc; Path=/")
			fmt.Fprint(w, body)
		})
	}
	mux.HandleFunc("/forbidden", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "<html><body>denied</body></html>")
	})
	mux.HandleFunc("/old.png", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/img/new.png", http.StatusFound)
	})
	mux.HandleFunc("/hang.png", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	mux.HandleFunc("/shared.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(testbrowser.PNG(30, 40))
	})
	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(testbrowser.PNG(30, 40))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func open(t *testing.T, ts *httptest.Server, path string) *Session {
	t.Helper()
	s, err := New(testbrowser.Start(t))
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.Open(ts.URL + path)
	if err != nil || status != 200 {
		t.Fatalf("Open = %d, %v", status, err)
	}
	if err := s.Scroll(); err != nil {
		t.Fatal(err)
	}
	return s
}

const gallery = `<html><head><title>Gallery</title></head><body style="margin:0">
<div id="main">
  <img src="/img/a.png" style="display:block;width:30px;height:40px">
  <img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" data-src="/img/lazy.png" style="display:block;width:30px;height:40px">
  <div style="width:30px;height:40px;background-image:url('/img/bg.png')"></div>
  <canvas id="c" width="30" height="80"></canvas>
</div>
<img src="/old.png" style="display:block">
<script>
  const i = new Image();
  i.onload = () => document.getElementById('c').getContext('2d').drawImage(i, 0, 40);
  i.src = '/img/canvas.png';
</script>
</body></html>`

func TestCollectFindsEveryKindWithPositions(t *testing.T) {
	ts := server(t, map[string]string{"/g": gallery})
	s := open(t, ts, "/g")
	time.Sleep(300 * time.Millisecond) // let the canvas image load and draw

	raws, invalid, err := s.Collect("")
	if err != nil || invalid {
		t.Fatalf("Collect: %v %v", invalid, err)
	}
	kinds := map[pick.Kind]int{}
	var canvas pick.Raw
	for _, r := range raws {
		kinds[r.Kind]++
		if r.Kind == pick.KindCanvas {
			canvas = r
		}
	}
	if kinds[pick.KindImg] != 3 || kinds[pick.KindBackground] != 1 || kinds[pick.KindCanvas] != 1 {
		t.Fatalf("kinds = %v (%+v)", kinds, raws)
	}
	if !strings.HasSuffix(canvas.Src, "/img/canvas.png") || canvas.W != 30 || canvas.H != 40 {
		t.Fatalf("canvas = %+v", canvas)
	}
	// canvas top is 120 (three 40px blocks), drawn at dy=40 on a 1:1 canvas
	if canvas.Y != 160 || canvas.BoxH != 40 || !canvas.Positioned {
		t.Fatalf("canvas position = %+v", canvas)
	}
	title, _ := s.Title()
	if title != "Gallery" {
		t.Fatalf("title = %q", title)
	}
}

func TestCollectScope(t *testing.T) {
	ts := server(t, map[string]string{"/g": gallery})
	s := open(t, ts, "/g")
	raws, _, _ := s.Collect("#main")
	if len(raws) == 0 {
		t.Fatal("no candidates collected")
	}
	var inScope, outScope int
	for _, r := range raws {
		in := !strings.HasSuffix(r.Src, "/old.png")
		if r.InScope != in {
			t.Fatalf("InScope wrong for %+v", r)
		}
		if r.InScope {
			inScope++
		} else {
			outScope++
		}
	}
	if inScope == 0 || outScope == 0 {
		t.Fatalf("scope split in=%d out=%d", inScope, outScope)
	}
	if _, invalid, err := s.Collect("#("); err != nil || !invalid {
		t.Fatalf("invalid selector: %v %v", invalid, err)
	}
	if ok, err := s.ValidSelector("#("); err != nil || ok {
		t.Fatalf("ValidSelector(#() = %v, %v", ok, err)
	}
	if ok, err := s.ValidSelector("#main img"); err != nil || !ok {
		t.Fatalf("ValidSelector(#main img) = %v, %v", ok, err)
	}
}

func TestCapturedFollowsRedirect(t *testing.T) {
	ts := server(t, map[string]string{"/g": gallery})
	s := open(t, ts, "/g")
	body, ct, ok := s.Captured(ts.URL + "/old.png")
	if !ok || !bytes.Equal(body, testbrowser.PNG(30, 40)) || ct != "image/png" {
		t.Fatalf("Captured = %d bytes, %q, %v", len(body), ct, ok)
	}
	if _, _, ok := s.Captured(ts.URL + "/img/never.png"); ok {
		t.Fatal("captured a URL that was never loaded")
	}
	net := s.NetworkImages()
	if len(net) < 4 || net[0].Kind != pick.KindNetwork {
		t.Fatalf("network images = %+v", net)
	}
	s.Reset()
	if len(s.NetworkImages()) != 0 {
		t.Fatal("Reset kept images")
	}
}

func TestFetchInPageAndCookies(t *testing.T) {
	ts := server(t, map[string]string{"/g": gallery})
	s := open(t, ts, "/g")
	body, ct, err := s.FetchInPage(ts.URL + "/img/x.png")
	if err != nil || !bytes.Equal(body, testbrowser.PNG(30, 40)) || ct != "image/png" {
		t.Fatalf("FetchInPage = %d bytes, %q, %v", len(body), ct, err)
	}
	if _, _, err := s.FetchInPage(ts.URL + "/missing"); err == nil || err.Error() != "HTTP 404" {
		t.Fatalf("404 err = %v", err)
	}
	if ua, err := s.UserAgent(); err != nil || ua == "" {
		t.Fatalf("UserAgent = %q, %v", ua, err)
	}
	if c, err := s.CookieHeader(ts.URL + "/img/x.png"); err != nil || !strings.Contains(c, "sid=abc") {
		t.Fatalf("CookieHeader = %q, %v", c, err)
	}
}

func TestClickNext(t *testing.T) {
	ts := server(t, map[string]string{
		"/p1":    `<html><body><button class="next" onclick="location.href='/p2'">다음 &gt;</button></body></html>`,
		"/p2":    `<html><head><title>Two</title></head><body><a href="/p1">이전</a><button>다음 &gt;</button><button>다음 &gt;</button></body></html>`,
		"/stuck": `<html><body><button class="next" data-locked="true">Next</button></body></html>`,
	})
	s := open(t, ts, "/p1")

	res, n, err := s.ClickNext("다음 >", 5*time.Second) // CSS syntax error → text match
	if err != nil || res != NextNavigated || n != 1 {
		t.Fatalf("text click = %v %d %v", res, n, err)
	}
	if u, _ := s.URL(); u != ts.URL+"/p2" {
		t.Fatalf("url = %q", u)
	}
	res, n, _ = s.ClickNext("다음 >", 200*time.Millisecond)
	if n != 2 || res != NextNoNavigation {
		t.Fatalf("two matches = %v %d", res, n)
	}
	if res, _, _ := s.ClickNext("button.missing", time.Second); res != NextNotFound {
		t.Fatalf("missing = %v", res)
	}

	if _, err := s.Open(ts.URL + "/stuck"); err != nil {
		t.Fatal(err)
	}
	if res, _, _ := s.ClickNext("button.next", 300*time.Millisecond); res != NextNoNavigation {
		t.Fatalf("locked = %v", res)
	}
}

func TestClickNextSlowPageStillNavigated(t *testing.T) {
	ts := server(t, map[string]string{
		"/p1":   `<html><body><button class="next" onclick="location.href='/slow'">Next</button></body></html>`,
		"/slow": `<html><body>two<img src="/hang.png"></body></html>`,
	})
	s := open(t, ts, "/p1")
	res, _, err := s.ClickNext("button.next", 2*time.Second)
	if err != nil || res != NextNavigated {
		t.Fatalf("slow page = %v %v", res, err)
	}
}

func TestOpenReportsStatus(t *testing.T) {
	ts := server(t, nil)
	s, err := New(testbrowser.Start(t))
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.Open(ts.URL + "/nope")
	if err != nil || status != 404 {
		t.Fatalf("Open = %d, %v", status, err)
	}
}

// statusOf polls briefly: the response recorder runs in its own goroutine.
func statusOf(s *Session, url string) int64 {
	for i := 0; i < 50; i++ {
		if st, ok := s.Status(url); ok {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0
}

func TestStatusAfterNext(t *testing.T) {
	ts := server(t, map[string]string{
		"/p1":   `<html><body><button class="next" onclick="location.href='/p2'">Next</button></body></html>`,
		"/p2":   `<html><body>two</body></html>`,
		"/deny": `<html><body><button class="next" onclick="location.href='/forbidden'">Next</button></body></html>`,
	})
	s := open(t, ts, "/p1")
	res, _, err := s.ClickNext("button.next", 5*time.Second)
	if err != nil || res != NextNavigated {
		t.Fatalf("click = %v %v", res, err)
	}
	u, _ := s.URL()
	if st := statusOf(s, u); st != 200 {
		t.Fatalf("status of %s = %d", u, st)
	}

	if _, err := s.Open(ts.URL + "/deny"); err != nil {
		t.Fatal(err)
	}
	res, _, err = s.ClickNext("button.next", 5*time.Second)
	if err != nil || res != NextNavigated {
		t.Fatalf("click = %v %v", res, err)
	}
	u, _ = s.URL()
	if st := statusOf(s, u); st != 403 {
		t.Fatalf("status of %s = %d", u, st)
	}
}

func TestLookupIsOrderIndependent(t *testing.T) {
	s := &Session{}
	s.Reset()
	ev := network.EventResponseReceived{
		RequestID: "r1",
		Type:      network.ResourceTypeImage,
		Response:  &network.Response{URL: "http://x/new.png", Status: 200, MimeType: "image/png"},
	}
	s.record(ev) // response first, redirect hop afterwards
	s.recordRequest("r1", "http://x/old.png")
	s.recordRequest("r1", "http://x/new.png")
	for _, u := range []string{"http://x/old.png", "http://x/new.png"} {
		if r, ok := s.lookup(u); !ok || r.status != 200 || r.mime != "image/png" {
			t.Fatalf("lookup(%s) = %+v, %v", u, r, ok)
		}
	}
	// a later failure for the same URL must not hide the good response
	s.record(network.EventResponseReceived{RequestID: "r2", Response: &network.Response{URL: "http://x/new.png", Status: 500}})
	if st, ok := s.Status("http://x/new.png"); !ok || st != 200 {
		t.Fatalf("Status = %d, %v", st, ok)
	}
	s.record(ev)
	if len(s.images) != 1 {
		t.Fatalf("images = %v", s.images)
	}
}

func TestLateResponseOfResetPageIsDropped(t *testing.T) {
	s := &Session{}
	s.Reset()
	img := func(id, url string) network.EventResponseReceived {
		return network.EventResponseReceived{
			RequestID: network.RequestID(id),
			LoaderID:  "L", // same loader throughout, like an SPA
			Type:      network.ResourceTypeImage,
			Response:  &network.Response{URL: url, Status: 200, MimeType: "image/png"},
		}
	}
	s.recordRequest("a1", "http://x/a1.png")
	s.recordRequest("a2", "http://x/a2.png")
	s.record(img("a1", "http://x/a1.png"))
	s.Reset()
	s.record(img("a2", "http://x/a2.png")) // trails in after the navigation started
	s.recordRequest("b1", "http://x/b1.png")
	s.record(img("b1", "http://x/b1.png")) // issued after Reset: kept despite the same loader
	if got := strings.Join(s.images, ","); got != "http://x/b1.png" {
		t.Fatalf("images = %q, want only the new page's", got)
	}
	if _, ok := s.Status("http://x/a2.png"); ok {
		t.Fatal("late response of the old page was recorded")
	}
	if st, ok := s.Status("http://x/b1.png"); !ok || st != 200 {
		t.Fatalf("Status(b1) = %d, %v", st, ok)
	}
}

func TestFetchInPageTimesOut(t *testing.T) {
	ts := server(t, map[string]string{"/g": gallery})
	s := open(t, ts, "/g")
	old := fetchTimeout
	fetchTimeout = 300 * time.Millisecond
	t.Cleanup(func() { fetchTimeout = old })
	start := time.Now()
	if _, _, err := s.FetchInPage(ts.URL + "/hang.png"); err == nil {
		t.Fatal("hanging fetch returned no error")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("returned after %v, want about the timeout", d)
	}
}

func TestRequestStartedBeforeResetIsStaleEvenWhenProcessedLater(t *testing.T) {
	s := &Session{}
	s.Reset()
	s.cutoff = 100 // the browser clock at Reset
	img := func(id, url string) network.EventResponseReceived {
		return network.EventResponseReceived{
			RequestID: network.RequestID(id),
			Type:      network.ResourceTypeImage,
			Response:  &network.Response{URL: url, Status: 200},
		}
	}
	// The response beats its request event, so it is recorded first and must
	// be taken back once the request turns out to predate Reset.
	s.record(img("slow", "http://x/slow.png"))
	s.recordRequest("slow", "http://x/slow.png")
	if !s.startedBeforeReset("slow", 99.5) {
		t.Fatal("request from before Reset was not marked stale")
	}
	if _, ok := s.Status("http://x/slow.png"); ok || len(s.images) != 0 || s.seen["http://x/slow.png"] {
		t.Fatalf("stale request left records: images=%v", s.images)
	}
	// A response that arrives after its stale request event is dropped.
	s.record(img("slow", "http://x/slow.png"))
	if len(s.images) != 0 {
		t.Fatalf("images = %v", s.images)
	}
	if s.startedBeforeReset("fresh", 100.5) {
		t.Fatal("request from after Reset was marked stale")
	}
	s.record(img("fresh", "http://x/fresh.png"))
	if got := strings.Join(s.images, ","); got != "http://x/fresh.png" {
		t.Fatalf("images = %q", got)
	}
}

func TestResetReadsBrowserClock(t *testing.T) {
	s, err := New(testbrowser.Start(t))
	if err != nil {
		t.Fatal(err)
	}
	if now := s.browserNow(); now <= 0 {
		t.Fatalf("browserNow = %v", now)
	}
	s.Reset()
	if s.cutoff <= 0 {
		t.Fatalf("cutoff after Reset = %v", s.cutoff)
	}
}

func TestCachedImageSharedByNextPageIsKept(t *testing.T) {
	ts := server(t, map[string]string{
		"/a": `<html><body><img src="/shared.png"><button class="next" onclick="location.href='/b'">Next</button></body></html>`,
		"/b": `<html><body><img src="/shared.png"></body></html>`,
	})
	s, err := New(testbrowser.Start(t))
	if err != nil {
		t.Fatal(err)
	}
	s.Reset()
	if st, err := s.Open(ts.URL + "/a"); err != nil || st != 200 {
		t.Fatalf("Open = %d, %v", st, err)
	}
	s.Reset()
	if res, _, err := s.ClickNext("button.next", 5*time.Second); err != nil || res != NextNavigated {
		t.Fatalf("ClickNext = %v, %v", res, err)
	}
	if st := statusOf(s, ts.URL+"/shared.png"); st != 200 {
		t.Fatalf("status of shared.png = %d", st)
	}
	imgs := s.NetworkImages()
	if len(imgs) != 1 || imgs[0].Src != ts.URL+"/shared.png" {
		t.Fatalf("network images = %v, want shared.png", imgs)
	}
}

func TestResetAcrossRedirectAndPushState(t *testing.T) {
	ts := server(t, map[string]string{
		"/p1": `<html><body><img src="/hang.png"><button class="next" onclick="location.href='/old.png'">Next</button></body></html>`,
		"/spa": `<html><body><script>onload=()=>{const h=new Image();h.src='/hang.png?x';document.body.appendChild(h)}</script>
<button class="next" onclick="history.pushState({},'','/spa2');const i=new Image();i.src='/img/after.png';document.body.appendChild(i)">Next</button></body></html>`,
	})
	s, err := New(testbrowser.Start(t))
	if err != nil {
		t.Fatal(err)
	}
	// Next lands on a redirect chain; the final URL's status must be found.
	s.Reset()
	if st, err := s.Open(ts.URL + "/p1"); err != nil || st != 200 {
		t.Fatalf("Open = %d, %v", st, err)
	}
	s.Reset()
	if res, _, err := s.ClickNext("button.next", 5*time.Second); err != nil || res != NextNavigated {
		t.Fatalf("ClickNext = %v, %v", res, err)
	}
	u, _ := s.URL()
	if st := statusOf(s, u); st != 200 {
		t.Fatalf("status of %s = %d", u, st)
	}

	// An SPA keeps its document: images it loads after Reset stay, while the
	// slow request it sent just before Reset must not leak in.
	s.Reset()
	if st, err := s.Open(ts.URL + "/spa"); err != nil || st != 200 {
		t.Fatalf("Open = %d, %v", st, err)
	}
	s.Reset()
	if res, _, err := s.ClickNext("button.next", 5*time.Second); err != nil || res != NextNavigated {
		t.Fatalf("ClickNext = %v, %v", res, err)
	}
	time.Sleep(3500 * time.Millisecond) // /hang.png answers after 3s
	imgs := s.NetworkImages()
	if len(imgs) != 1 || imgs[0].Src != ts.URL+"/img/after.png" {
		t.Fatalf("network images = %v, want only after.png", imgs)
	}
	if _, ok := s.Status(ts.URL + "/hang.png?x"); ok {
		t.Fatal("response to a request sent before Reset was recorded")
	}
}
