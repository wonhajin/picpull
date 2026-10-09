// Package pick turns what a page reported into the list of images to save:
// it chooses the real URL of each element, applies the filters and sorts by
// position on screen.
package pick

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Kind string

const (
	KindImg        Kind = "img"
	KindBackground Kind = "background"
	KindCanvas     Kind = "canvas"
	KindNetwork    Kind = "network"
)

// Raw is one image as the page script reports it, before URL choice.
type Raw struct {
	Kind         Kind    `json:"kind"`
	Src          string  `json:"src"`
	CurrentSrc   string  `json:"currentSrc"`
	Srcset       string  `json:"srcset"`
	DataSrc      string  `json:"dataSrc"`
	DataOriginal string  `json:"dataOriginal"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	BoxH         float64 `json:"boxH"`
	W            int     `json:"w"`
	H            int     `json:"h"`
	Positioned   bool    `json:"positioned"`
	InScope      bool    `json:"inScope"`
}

// Candidate is an image to save. X, Y and BoxH are page coordinates of its
// box; Width and Height are the image's own size, 0 when unknown.
type Candidate struct {
	URL           string
	Kind          Kind
	X, Y, BoxH    float64
	Width, Height int
	Positioned    bool
	InScope       bool
}

type Filter struct {
	MinSize int
	Match   *regexp.Regexp
	Scoped  bool // --in was given: keep only candidates inside the area
}

// Resolve picks the real URL of each raw image. Lazy-load pages keep the real
// image in data-* and a placeholder in src, so data-* wins.
func Resolve(raws []Raw, baseURL string) []Candidate {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	out := make([]Candidate, 0, len(raws))
	for _, r := range raws {
		chosen := firstUsable(r.DataOriginal, r.DataSrc, PickSrcset(r.Srcset), r.CurrentSrc, r.Src)
		abs := absolute(base, chosen)
		if abs == "" {
			continue
		}
		c := Candidate{URL: abs, Kind: r.Kind, X: r.X, Y: r.Y, BoxH: r.BoxH,
			Positioned: r.Positioned, InScope: r.InScope}
		// The element's natural size belongs to whatever it loaded. If that is
		// a placeholder, the chosen image's size is unknown until downloaded.
		loaded := absolute(base, firstUsable(r.CurrentSrc, r.Src))
		if r.Kind == KindCanvas || loaded == abs {
			c.Width, c.Height = r.W, r.H
		}
		out = append(out, c)
	}
	return out
}

func firstUsable(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !strings.HasPrefix(v, "data:") && !strings.HasPrefix(v, "blob:") {
			return v
		}
	}
	return ""
}

func absolute(base *url.URL, ref string) string {
	if ref == "" {
		return ""
	}
	u, err := base.Parse(ref)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

// PickSrcset returns the largest candidate by its w or x descriptor, or the
// last one when none has a descriptor. Parses per HTML srcset grammar to
// handle URLs containing commas (e.g., CDN URLs).
// Note: a URL ending in comma has no descriptor; commas inside a URL are kept.
func PickSrcset(srcset string) string {
	best, bestN := "", -1.0
	for i := 0; i < len(srcset); {
		for i < len(srcset) && (isSpace(srcset[i]) || srcset[i] == ',') {
			i++
		}
		if i >= len(srcset) {
			break
		}

		urlStart := i
		for i < len(srcset) && !isSpace(srcset[i]) {
			i++
		}
		url := srcset[urlStart:i]

		hasTrailingComma := strings.HasSuffix(url, ",")
		url = strings.TrimRight(url, ",")

		n := 0.0
		if !hasTrailingComma {
			for i < len(srcset) && isSpace(srcset[i]) {
				i++
			}

			descStart := i
			for i < len(srcset) && srcset[i] != ',' {
				i++
			}
			desc := strings.TrimSpace(srcset[descStart:i])

			if desc != "" {
				if v, err := strconv.ParseFloat(desc[:len(desc)-1], 64); err == nil {
					n = v
				}
			}
		}

		if n >= bestN {
			best, bestN = url, n
		}
	}
	return best
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

// Select filters, sorts by screen position and removes duplicates. Positioned
// candidates come first in reading order; the rest keep their discovery
// order after them.
func Select(cands []Candidate, f Filter) []Candidate {
	var placed, unplaced []Candidate
	for _, c := range cands {
		if !keep(c, f) {
			continue
		}
		if c.Positioned {
			placed = append(placed, c)
		} else {
			unplaced = append(unplaced, c)
		}
	}
	ordered := append(readingOrder(placed), unplaced...)
	seen := map[string]bool{}
	out := ordered[:0]
	for _, c := range ordered {
		if seen[c.URL] {
			continue
		}
		seen[c.URL] = true
		out = append(out, c)
	}
	return out
}

func keep(c Candidate, f Filter) bool {
	if f.Scoped && !c.InScope {
		return false
	}
	if f.Match != nil && !f.Match.MatchString(c.URL) {
		return false
	}
	// Unknown sizes pass here; the downloader checks them on the bytes.
	if f.MinSize > 0 && c.Width > 0 && c.Height > 0 && (c.Width < f.MinSize || c.Height < f.MinSize) {
		return false
	}
	return true
}

// readingOrder groups boxes into rows by vertical overlap, then reads rows
// top to bottom and each row left to right. Comparing tops alone would put a
// taller image on the right before its shorter neighbor on the left. A box
// joins a row only if it starts above the bottom of every box already in it,
// so one tall box (a background wrapper) cannot swallow the rows below it.
func readingOrder(cs []Candidate) []Candidate {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Y < cs[j].Y })
	var out []Candidate
	for i := 0; i < len(cs); {
		bottom := cs[i].Y + cs[i].BoxH
		j := i + 1
		for j < len(cs) && cs[j].Y < bottom {
			bottom = min(bottom, cs[j].Y+cs[j].BoxH)
			j++
		}
		row := cs[i:j]
		sort.SliceStable(row, func(a, b int) bool { return row[a].X < row[b].X })
		out = append(out, row...)
		i = j
	}
	return out
}
