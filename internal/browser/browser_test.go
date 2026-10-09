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

const macChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

func TestFindMac(t *testing.T) {
	b, err := fakeFinder("darwin", []string{macChrome}, nil).find()
	if err != nil || b.Path != macChrome {
		t.Fatalf("got %+v %v", b, err)
	}
}

func TestFindUserApplications(t *testing.T) {
	p := "/Users/u/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	b, err := fakeFinder("darwin", []string{p}, nil).find()
	if err != nil || b.Path != p {
		t.Fatalf("got %+v %v", b, err)
	}
}

func TestFindLinuxPath(t *testing.T) {
	b, err := fakeFinder("linux", nil, map[string]string{"google-chrome": "/usr/bin/google-chrome"}).find()
	if err != nil || b.Path != "/usr/bin/google-chrome" {
		t.Fatalf("got %+v %v", b, err)
	}
	b, err = fakeFinder("linux", nil, map[string]string{"google-chrome-stable": "/usr/bin/gcs"}).find()
	if err != nil || b.Path != "/usr/bin/gcs" {
		t.Fatalf("got %+v %v", b, err)
	}
}

func TestFindNothing(t *testing.T) {
	_, err := fakeFinder("darwin", nil, nil).find()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	want := "Google Chrome not found. Install Google Chrome and try again."
	if err.Error() != want {
		t.Fatalf("message = %q", err.Error())
	}
}
