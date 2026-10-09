package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/wonhj12/picpull/internal/page"
)

const attempts = 3

// fetch returns an image's bytes: the browser's own copy first, then a
// request from inside the page, then a plain HTTP request with the page's
// cookies. cached reports that the first path worked.
func fetch(ctx context.Context, d Deps, s *page.Session, url, referer string) (body []byte, contentType string, cached bool, err error) {
	if b, ct, ok := s.Captured(url); ok {
		return b, ct, true, nil
	}
	for i := range attempts {
		if i > 0 {
			d.Sleep(ctx, time.Second)
			if ctx.Err() != nil {
				return nil, "", false, ctx.Err()
			}
		}
		if body, contentType, err = s.FetchInPage(url); err == nil {
			return body, contentType, false, nil
		}
		if ctx.Err() != nil {
			return nil, "", false, ctx.Err()
		}
		// Cross-origin images often fail in-page because of CORS.
		if body, contentType, err = fetchHTTP(ctx, s, d.HTTP, url, referer); err == nil {
			return body, contentType, false, nil
		}
	}
	return nil, "", false, err
}

func fetchHTTP(ctx context.Context, s *page.Session, client *http.Client, url, referer string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	// Many image hosts refuse requests without the page as Referer.
	req.Header.Set("Referer", referer)
	if ua, err := s.UserAgent(); err == nil {
		req.Header.Set("User-Agent", ua)
	}
	if c, err := s.CookieHeader(url); err == nil && c != "" {
		req.Header.Set("Cookie", c)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	if len(body) == 0 {
		return nil, "", errors.New("empty response")
	}
	return body, resp.Header.Get("Content-Type"), nil
}
