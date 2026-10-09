package pick

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"regexp"
	"testing"
)

func urls(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.URL
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestPickSrcset(t *testing.T) {
	cases := map[string]string{
		"a.jpg 300w, b.jpg 1200w, c.jpg 600w": "b.jpg",
		"a.jpg 1x, b.jpg 2x":                  "b.jpg",
		"a.jpg, b.jpg":                        "b.jpg",
		"":                                    "",
		// URLs with commas (e.g., CDN URLs like Cloudinary)
		"https://res.cloudinary.com/x/image/upload/w_100,h_100/a.jpg 100w, https://res.cloudinary.com/x/image/upload/w_800,h_800/a.jpg 800w": "https://res.cloudinary.com/x/image/upload/w_800,h_800/a.jpg",
		"a.jpg,b.jpg 100w": "a.jpg,b.jpg",
		"a.jpg, b.jpg 2x":  "b.jpg",
	}
	for in, want := range cases {
		if got := PickSrcset(in); got != want {
			t.Errorf("PickSrcset(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveLazyPlaceholder(t *testing.T) {
	raws := []Raw{{
		Kind: KindImg, Src: "data:image/gif;base64,R0lGOD", CurrentSrc: "data:image/gif;base64,R0lGOD",
		DataSrc: "/real/1.jpg", W: 1, H: 1, Positioned: true,
	}}
	got := Resolve(raws, "https://e.com/view/1")
	if len(got) != 1 || got[0].URL != "https://e.com/real/1.jpg" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Width != 0 || got[0].Height != 0 {
		t.Fatalf("placeholder size leaked: %+v", got[0])
	}
}

func TestResolveKeepsSizeWhenLoadedURLChosen(t *testing.T) {
	raws := []Raw{{Kind: KindImg, Src: "a.jpg", CurrentSrc: "https://e.com/a.jpg", W: 800, H: 600}}
	got := Resolve(raws, "https://e.com/")
	if got[0].Width != 800 || got[0].Height != 600 {
		t.Fatalf("got %+v", got[0])
	}
}

func TestResolveSkipsDataBlobEmptyAndFragments(t *testing.T) {
	raws := []Raw{
		{Kind: KindImg, Src: "data:image/png;base64,AA"},
		{Kind: KindImg, Src: "blob:https://e.com/123"},
		{Kind: KindImg},
		{Kind: KindBackground, Src: "/bg.png#x"},
	}
	equal(t, urls(Resolve(raws, "https://e.com/")), []string{"https://e.com/bg.png"})
}

func TestSelectOrdersByScreenPosition(t *testing.T) {
	cands := []Candidate{
		{URL: "net", Kind: KindNetwork},
		{URL: "bottom", Positioned: true, X: 0, Y: 500, BoxH: 100},
		{URL: "right", Positioned: true, X: 300, Y: 0, BoxH: 100},
		{URL: "left-taller", Positioned: true, X: 0, Y: -20, BoxH: 140},
		{URL: "hidden", Kind: KindImg},
	}
	equal(t, urls(Select(cands, Filter{})), []string{"left-taller", "right", "bottom", "net", "hidden"})
}

func TestSelectStackedStripsStayVertical(t *testing.T) {
	cands := []Candidate{
		{URL: "2", Positioned: true, X: 0, Y: 1000, BoxH: 1000},
		{URL: "1", Positioned: true, X: 0, Y: 0, BoxH: 1000},
		{URL: "3", Positioned: true, X: 0, Y: 2000, BoxH: 1000},
	}
	equal(t, urls(Select(cands, Filter{})), []string{"1", "2", "3"})
}

func TestSelectDedupesKeepingFirstOnScreen(t *testing.T) {
	cands := []Candidate{
		{URL: "a", Kind: KindNetwork},
		{URL: "a", Positioned: true, Y: 10, BoxH: 10},
		{URL: "b", Positioned: true, Y: 0, BoxH: 5},
	}
	equal(t, urls(Select(cands, Filter{})), []string{"b", "a"})
}

func TestSelectFilters(t *testing.T) {
	cands := []Candidate{
		{URL: "https://e/1.jpg", Width: 800, Height: 1200, InScope: true},
		{URL: "https://e/logo.png", Width: 800, Height: 1200, InScope: true},
		{URL: "https://e/2.jpg", Width: 800, Height: 100, InScope: true},
		{URL: "https://e/3.jpg", InScope: true},
		{URL: "https://e/4.jpg", Width: 800, Height: 1200, InScope: false},
	}
	f := Filter{MinSize: 200, Match: regexp.MustCompile(`/\d+\.jpg$`), Scoped: true}
	equal(t, urls(Select(cands, f)), []string{"https://e/1.jpg", "https://e/3.jpg"})
	equal(t, urls(Select(cands, Filter{})), []string{
		"https://e/1.jpg", "https://e/logo.png", "https://e/2.jpg", "https://e/3.jpg", "https://e/4.jpg"})
}

func TestDecodedSize(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 30, 40)))
	if w, h, ok := DecodedSize(b.Bytes()); !ok || w != 30 || h != 40 {
		t.Fatalf("png = %d %d %v", w, h, ok)
	}
	if _, _, ok := DecodedSize([]byte("not an image")); ok {
		t.Fatal("garbage decoded")
	}
}

func TestDecodedSizeJPEGGIFWebP(t *testing.T) {
	var jb, gb bytes.Buffer
	jpeg.Encode(&jb, image.NewRGBA(image.Rect(0, 0, 30, 40)), nil)
	gif.Encode(&gb, image.NewPaletted(image.Rect(0, 0, 30, 40), []color.Color{color.Black}), nil)

	// A lossless WebP header is enough: DecodeConfig never reads the pixels.
	webp := []byte("RIFF\x12\x00\x00\x00WEBPVP8L\x05\x00\x00\x00\x2f")
	webp = binary.LittleEndian.AppendUint32(webp, 29|39<<14)
	webp = append(webp, 0) // chunk padding

	for name, body := range map[string][]byte{"jpeg": jb.Bytes(), "gif": gb.Bytes(), "webp": webp} {
		if w, h, ok := DecodedSize(body); !ok || w != 30 || h != 40 {
			t.Errorf("%s = %d %d %v", name, w, h, ok)
		}
	}
}

func TestSelectScopedDropsNetworkCandidates(t *testing.T) {
	cands := []Candidate{
		{URL: "https://e/dom.jpg", Kind: KindImg, InScope: true},
		{URL: "https://e/net.jpg", Kind: KindNetwork},
	}
	equal(t, urls(Select(cands, Filter{Scoped: true})), []string{"https://e/dom.jpg"})
	equal(t, urls(Select(cands, Filter{})), []string{"https://e/dom.jpg", "https://e/net.jpg"})
}

func TestSelectMinSizeKeepsHeightOnlyUnknown(t *testing.T) {
	cands := []Candidate{
		{URL: "https://e/a.jpg", Width: 50},
		{URL: "https://e/b.jpg", Height: 50},
	}
	equal(t, urls(Select(cands, Filter{MinSize: 200})), []string{"https://e/a.jpg", "https://e/b.jpg"})
}

func TestSelectTallBackgroundDoesNotMergeGridRows(t *testing.T) {
	cands := []Candidate{
		{URL: "bg", Positioned: true, X: 0, Y: 0, BoxH: 5000},
		{URL: "d", Positioned: true, X: 300, Y: 400, BoxH: 100},
		{URL: "b", Positioned: true, X: 300, Y: 100, BoxH: 100},
		{URL: "c", Positioned: true, X: 0, Y: 400, BoxH: 100},
		{URL: "a", Positioned: true, X: 0, Y: 100, BoxH: 100},
	}
	equal(t, urls(Select(cands, Filter{})), []string{"bg", "a", "b", "c", "d"})
}
