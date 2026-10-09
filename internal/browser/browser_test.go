package browser

import (
	"errors"
	"testing"
)

func fakeFinder(goos string, files []string, onPath map[string]string) finder {
	set := map[string]bool{}
	for _, f := range files {
		set[f] = true
	}
	return finder{
		goos:   goos,
		home:   "/Users/u",
		exists: func(p string) bool { return set[p] },
		lookPath: func(name string) (string, error) {
			if p, ok := onPath[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
	}
}

const (
	macChromium = "/Applications/Chromium.app/Contents/MacOS/Chromium"
	macChrome   = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
)

func TestFindPrefersChromium(t *testing.T) {
	b, fellBack, err := fakeFinder("darwin", []string{macChromium, macChrome}, nil).find("")
	if err != nil || b.Name != "Chromium" || b.Path != macChromium || fellBack {
		t.Fatalf("got %+v %v %v", b, fellBack, err)
	}
}

func TestFindFallsBackToChrome(t *testing.T) {
	b, fellBack, err := fakeFinder("darwin", []string{macChrome}, nil).find("")
	if err != nil || b.Name != "Chrome" || !fellBack {
		t.Fatalf("got %+v %v %v", b, fellBack, err)
	}
}

func TestFindUserApplications(t *testing.T) {
	p := "/Users/u/Applications/Chromium.app/Contents/MacOS/Chromium"
	b, _, err := fakeFinder("darwin", []string{p}, nil).find("")
	if err != nil || b.Path != p {
		t.Fatalf("got %+v %v", b, err)
	}
}

func TestFindForcedBrowserDoesNotFallBack(t *testing.T) {
	_, _, err := fakeFinder("darwin", []string{macChromium}, nil).find("chrome")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	b, fellBack, err := fakeFinder("darwin", []string{macChromium, macChrome}, nil).find("chrome")
	if err != nil || b.Name != "Chrome" || fellBack {
		t.Fatalf("got %+v %v %v", b, fellBack, err)
	}
}

func TestFindLinuxPath(t *testing.T) {
	b, _, err := fakeFinder("linux", nil, map[string]string{"chromium-browser": "/usr/bin/chromium-browser"}).find("")
	if err != nil || b.Path != "/usr/bin/chromium-browser" || b.Name != "Chromium" {
		t.Fatalf("got %+v %v", b, err)
	}
	b, _, err = fakeFinder("linux", nil, map[string]string{"google-chrome-stable": "/usr/bin/gcs"}).find("")
	if err != nil || b.Name != "Chrome" {
		t.Fatalf("got %+v %v", b, err)
	}
}

func TestFindNothing(t *testing.T) {
	_, _, err := fakeFinder("darwin", nil, nil).find("")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	want := "Chromium or Chrome browser not found. Install Chromium or Google Chrome and try again."
	if err.Error() != want {
		t.Fatalf("message = %q", err.Error())
	}
}
