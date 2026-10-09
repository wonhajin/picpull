package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeName(t *testing.T) {
	// picpull targets macOS and Linux: only path separators, ":" and
	// control characters must go. "|" and "?" are legal there.
	cases := map[string]string{
		"Episode 12 | Site":   "Episode 12 | Site",
		"a/b:c\\d":            "a b c d",
		"  ..hidden.  ":       "hidden",
		"tab\tand\nnewline":   "tab and newline",
		"이모지 😀 제목":            "이모지 😀 제목",
		"":                    "",
		"a\x1b]52;c;x\x07b":   "a ]52;c;x b",
		"c1\u009bctl\u0085x":  "c1 ctl x",
		"bidi\u202eexe\u2066": "bidi exe",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeNameByteLimit(t *testing.T) {
	// Original case: Hangul repeated (3 bytes each).
	// 150 bytes is a multiple of 3, so cut never lands mid-rune and backoff is never exercised.
	long := strings.Repeat("가", 200) // 600 bytes
	got := SanitizeName(long)
	if len(got) > 150 {
		t.Fatalf("len = %d bytes, want <= 150", len(got))
	}
	if !strings.HasPrefix(long, got) || strings.ContainsRune(got, '�') {
		t.Fatalf("cut inside a rune: %q", got)
	}

	// Case where cut lands mid-rune: "a" (1 byte) + "가" repeated (3 bytes each).
	// Cut at 150 would land in the middle of a 3-byte Hangul character.
	// Backoff loop should find the largest rune boundary <= 150, which is byte 148.
	midRune := "a" + strings.Repeat("가", 100) // 1 + 300 = 301 bytes
	got = SanitizeName(midRune)
	if !utf8.ValidString(got) {
		t.Fatalf("cut inside a rune: %q", got)
	}
	if len(got) > 150 {
		t.Fatalf("len = %d bytes, want <= 150", len(got))
	}
	if len(got) != 148 {
		t.Fatalf("len = %d bytes, want exactly 148 (largest rune boundary <= 150)", len(got))
	}

	// Case with 4-byte emoji: emoji repeated (4 bytes each).
	// Cut at 150 could land in the middle of a 4-byte emoji.
	emoji := strings.Repeat("😀", 50) // 200 bytes
	got = SanitizeName(emoji)
	if !utf8.ValidString(got) {
		t.Fatalf("cut inside a rune: %q", got)
	}
	if len(got) > 150 {
		t.Fatalf("len = %d bytes, want <= 150", len(got))
	}
}

func TestPageDir(t *testing.T) {
	if got := PageDir("/r", 3, false); got != "/r" {
		t.Errorf("single = %q", got)
	}
	if got := PageDir("/r", 3, true); got != filepath.Join("/r", "003") {
		t.Errorf("multi = %q", got)
	}
}

func TestNamer(t *testing.T) {
	n := NewNamer(false)
	if got := n.Stem(7, "https://x/a.jpg"); got != "007" {
		t.Errorf("numbered = %q", got)
	}
	k := NewNamer(true)
	got := []string{
		k.Stem(1, "https://x/p/photo.jpg?w=300"),
		k.Stem(2, "https://y/q/photo.png"),
		k.Stem(3, "https://y/"),
		k.Stem(4, "https://y/a%20b.webp"),
	}
	want := []string{"photo", "photo-2", "003", "a b"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("keep-names stem %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNamerAvoidsCollisions(t *testing.T) {
	k := NewNamer(true)
	got := []string{
		k.Stem(1, "https://x/Photo.jpg"),
		k.Stem(2, "https://x/photo.jpg"),
		k.Stem(3, "https://x/photo-2.jpg"),
		k.Stem(4, "https://x/photo.png"),
		k.Stem(5, "https://x/005.jpg"),
		k.Stem(6, "https://y/"),
		k.Stem(7, "https://x/007.jpg"),
		k.Stem(8, "https://y/"),
	}
	want := []string{"Photo", "photo-2", "photo-2-2", "photo-3", "005", "006", "007", "008"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("stem %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}

func TestExistsIgnoresPart(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "001.jpg.part"), []byte("x"), 0o644)
	if Exists(dir, "001") {
		t.Fatal(".part counted as saved")
	}
	os.WriteFile(filepath.Join(dir, "001.jpg"), []byte("x"), 0o644)
	if !Exists(dir, "001") {
		t.Fatal("saved file not found")
	}
	if Exists(dir, "00") || Exists(filepath.Join(dir, "missing"), "001") {
		t.Fatal("false positive")
	}
	os.WriteFile(filepath.Join(dir, "[a].png"), []byte("x"), 0o644)
	if !Exists(dir, "[a]") {
		t.Fatal("glob characters in stem broke lookup")
	}
	// Directories should not count as existing files
	os.Mkdir(filepath.Join(dir, "002"), 0o755)
	if Exists(dir, "002") {
		t.Fatal("directory counted as saved file")
	}
}

func TestExtOf(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	long := "https://x/a." + strings.Repeat("z", 300)
	cases := []struct {
		url, ct string
		body    []byte
		want    string
	}{
		{"https://x/a.JPG?w=1", "", nil, ".jpg"},
		{"https://x/a", "image/webp; charset=x", nil, ".webp"},
		{"https://x/a", "", nil, ".jpg"},
		{"https://x/a.png", "image/jpeg", nil, ".png"},
		{"https://x/a.tiff", "", nil, ".tiff"},
		{"https://x/image.php?id=3", "image/png", nil, ".png"},
		{"https://x/image.php?id=3", "", png, ".png"},
		{"https://x/image.php?id=3", "text/html", png, ".png"},
		{"https://x/image.php?id=3", "", nil, ".jpg"},
		{"https://x/icon", "", []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"), ".svg"},
		{"https://x/icon", "text/plain", []byte("\xef\xbb\xbf\n <?xml version=\"1.0\"?><SVG></SVG>"), ".svg"},
		{"https://x/icon", "", []byte("<?xml version=\"1.0\"?><rss></rss>"), ".jpg"},
		{"https://x/icon", "", []byte("<!doctype html><html><svg></svg></html>"), ".jpg"},
		{"https://x/a.", "image/gif", nil, ".gif"},
		{"https://x/a.", "", nil, ".jpg"},
		{long, "image/webp", nil, ".webp"},
		{long, "", nil, ".jpg"},
	}
	for _, c := range cases {
		if got := ExtOf(c.url, c.ct, c.body); got != c.want {
			t.Errorf("ExtOf(%q,%q) = %q, want %q", c.url, c.ct, got, c.want)
		}
	}
}

func TestSaveAndRemoveParts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	name, err := Save(dir, "001", "https://x/i", "image/png", []byte("png"))
	if err != nil || name != "001.png" {
		t.Fatalf("Save = %q, %v", name, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "001.png")); string(b) != "png" {
		t.Fatalf("content = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "001.png.part")); !os.IsNotExist(err) {
		t.Fatal(".part left behind")
	}
	os.WriteFile(filepath.Join(dir, "002.jpg.part"), []byte("x"), 0o644)
	if err := RemoveParts(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "001.png" {
		t.Fatalf("entries = %v", entries)
	}
	if err := RemoveParts(filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("missing dir: %v", err)
	}
}
