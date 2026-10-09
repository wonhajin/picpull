package main

import (
	"bytes"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errw bytes.Buffer
	code := run([]string{"version"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got, want := out.String(), "picpull dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
