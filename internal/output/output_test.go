package output

import (
	"bytes"
	"strings"
	"testing"
)

func newPlain(level Level) (*Printer, *bytes.Buffer, *bytes.Buffer) {
	var out, errw bytes.Buffer
	return New(&out, &errw, level, false, false, 0), &out, &errw
}

func TestNormalSinglePage(t *testing.T) {
	p, out, errw := newPlain(Normal)
	p.PageStart(1, false, "Sunset Gallery", "example.com")
	p.PageURL("https://example.com/a")
	p.Found(42)
	p.Image(Downloaded, "https://example.com/1.jpg", "001.jpg")
	p.Progress(1, 42)
	p.PageSummary(Counts{Success: 40, Skip: 2})
	p.Done(1, false, Counts{Success: 40, Skip: 2}, nil)

	want := "▸ Sunset Gallery — example.com\n" +
		"  Found 42 images\n" +
		"  ✓ 40 success · 2 skip · 0 fail\n" +
		"\nDone · 40 success · 2 skip · 0 fail\n"
	if out.String() != want {
		t.Fatalf("stdout =\n%q\nwant\n%q", out.String(), want)
	}
	if errw.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errw.String())
	}
}

func TestNormalMultiPageWithFailures(t *testing.T) {
	p, out, errw := newPlain(Normal)
	p.PageStart(2, true, "Episode 13", "example.com")
	p.Found(1)
	p.NoImages()
	p.Warn("--next matched 3 elements; clicked the first one.")
	p.Stopped("next button did not navigate.", false)
	p.Done(3, true, Counts{Success: 77, Fail: 2}, []string{"https://x/7.jpg", "https://x/22.jpg"})

	wantOut := "▸ [002] Episode 13 — example.com\n" +
		"  Found 1 image\n" +
		"Stopped: next button did not navigate.\n" +
		"\nDone · 3 pages · 77 success · 0 skip · 2 fail\n"
	wantErr := "  ! No images found. Try --all-network or check filters.\n" +
		"  ! --next matched 3 elements; clicked the first one.\n" +
		"Failed:\n  https://x/7.jpg\n  https://x/22.jpg\n"
	if out.String() != wantOut {
		t.Fatalf("stdout =\n%q\nwant\n%q", out.String(), wantOut)
	}
	if errw.String() != wantErr {
		t.Fatalf("stderr =\n%q\nwant\n%q", errw.String(), wantErr)
	}
}

func TestDonePagePlural(t *testing.T) {
	p, out, _ := newPlain(Normal)
	p.Done(1, true, Counts{}, nil)
	if !strings.Contains(out.String(), "Done · 1 page · ") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestVerboseImageLines(t *testing.T) {
	p, out, errw := newPlain(Verbose)
	p.PageURL("https://example.com/view/12")
	p.Image(Downloaded, "https://c/1.jpg", "001.jpg")
	p.Image(Cached, "https://c/2.jpg", "002.jpg")
	p.Image(Skipped, "https://c/3.jpg", "exists")
	p.Image(Failed, "https://c/4.jpg", "HTTP 404")

	wantOut := "  URL https://example.com/view/12\n" +
		"  downloading https://c/1.jpg → 001.jpg\n" +
		"  cached      https://c/2.jpg → 002.jpg\n" +
		"  skip        https://c/3.jpg (exists)\n"
	if out.String() != wantOut {
		t.Fatalf("stdout =\n%q\nwant\n%q", out.String(), wantOut)
	}
	if errw.String() != "  failed      https://c/4.jpg: HTTP 404\n" {
		t.Fatalf("stderr = %q", errw.String())
	}
}

func TestQuietShowsOnlySummaryAndErrors(t *testing.T) {
	p, out, errw := newPlain(Quiet)
	p.PageStart(1, false, "T", "h")
	p.Found(3)
	p.NoImages()
	p.PageSummary(Counts{Success: 3})
	p.Stopped("next button not found.", false)
	p.Stopped("page returned HTTP 403 (https://h/).", true)
	p.Done(1, false, Counts{Success: 3}, nil)

	if out.String() != "Done · 3 success · 0 skip · 0 fail\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if errw.String() != "Stopped: page returned HTTP 403 (https://h/).\n" {
		t.Fatalf("stderr = %q", errw.String())
	}
}

func TestErrorAndInterrupted(t *testing.T) {
	p, _, errw := newPlain(Quiet)
	p.Error("missing URL")
	p.Interrupted()
	if errw.String() != "Error: missing URL\nInterrupted.\n" {
		t.Fatalf("stderr = %q", errw.String())
	}
}

func TestProgressBarOnTerminalOnly(t *testing.T) {
	var out, errw bytes.Buffer
	p := New(&out, &errw, Normal, true, false, 80)
	p.Progress(14, 28)
	if !strings.Contains(out.String(), "14/28") || !strings.Contains(out.String(), "━") {
		t.Fatalf("tty progress = %q", out.String())
	}
	out.Reset()
	p.PageSummary(Counts{Success: 28})
	if !strings.HasPrefix(out.String(), "\r\x1b[K") {
		t.Fatalf("summary should clear the bar first: %q", out.String())
	}

	plain, pout, _ := newPlain(Normal)
	plain.Progress(14, 28)
	if pout.Len() != 0 {
		t.Fatalf("non-tty progress should print nothing, got %q", pout.String())
	}
}

func TestTitleTruncatedToTerminalWidth(t *testing.T) {
	var out, errw bytes.Buffer
	p := New(&out, &errw, Normal, true, false, 30)
	p.PageStart(1, false, "가나다라마바사아자차카타파하", "example.com")
	line := strings.TrimSuffix(out.String(), "\n")
	if !strings.Contains(line, "…") || !strings.HasSuffix(line, " — example.com") {
		t.Fatalf("line = %q", line)
	}
	if w := displayWidth(line); w > 30 {
		t.Fatalf("display width = %d, want <= 30 (%q)", w, line)
	}
}

func TestColorOnlyWhenEnabled(t *testing.T) {
	var out, errw bytes.Buffer
	p := New(&out, &errw, Normal, true, true, 80)
	p.PageSummary(Counts{Success: 1})
	if !strings.Contains(out.String(), "\x1b[32m✓\x1b[0m") {
		t.Fatalf("colored summary = %q", out.String())
	}
}
