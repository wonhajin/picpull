package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errw bytes.Buffer
	code := run([]string{"version"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got, want := out.String(), "picpull dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunUsageError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errw bytes.Buffer
	code := run([]string{"example.com"}, &out, &errw)
	want := "Error: URL must start with http:// or https://\nRun 'picpull --help' for usage.\n"
	if code != 2 || errw.String() != want {
		t.Fatalf("code = %d stderr = %q", code, errw.String())
	}
}

func TestRunHelp(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, errw bytes.Buffer
	if code := run([]string{"-h"}, &out, &errw); code != 0 || !strings.HasPrefix(out.String(), "Usage: picpull <url> [flags]") {
		t.Fatalf("code = %d stdout = %q", code, out.String())
	}
}
