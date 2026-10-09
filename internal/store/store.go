// Package store names folders and files and writes images so that a rerun
// of the same command resumes where it stopped.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxNameBytes keeps folder names well under the 255-byte limit of APFS and
// ext4; Hangul takes 3 bytes per character.
const maxNameBytes = 150

var contentTypeExt = map[string]string{
	"image/jpeg":    ".jpg",
	"image/png":     ".png",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/avif":    ".avif",
	"image/svg+xml": ".svg",
	"image/bmp":     ".bmp",
	"image/x-icon":  ".ico",
	"image/heic":    ".heic",
	"image/heif":    ".heif",
	"image/tiff":    ".tiff",
}

// urlExt lists the URL extensions trusted as image extensions; anything else
// (".php", "." or a long garbage suffix) falls back to the content type.
var urlExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".avif": true, ".svg": true, ".bmp": true, ".ico": true, ".heic": true,
	".heif": true, ".tif": true, ".tiff": true,
}

// SanitizeName turns a page title into a folder or file name. Only what
// macOS and Linux reject or mishandle is removed: path separators, ":"
// (shown as "/" in Finder), control characters and bidi overrides that can
// disguise a name.
func SanitizeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r < 0x20 || (r >= 0x7f && r <= 0x9f) || isBidiControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, ". ")
	if len(s) > maxNameBytes {
		cut := maxNameBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = strings.TrimRight(s[:cut], ". ")
	}
	return s
}

func isBidiControl(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

func PageDir(root string, index int, multi bool) string {
	if !multi {
		return root
	}
	return filepath.Join(root, fmt.Sprintf("%03d", index))
}

// Namer hands out file stems for one page folder.
type Namer struct {
	keep bool
	// taken holds every stem handed out, lowercased: APFS ignores case, so
	// Photo and photo would overwrite each other.
	taken map[string]bool
	// next is the next numeric suffix to try for a lowercased base.
	next map[string]int
}

func NewNamer(keepNames bool) *Namer {
	return &Namer{keep: keepNames, taken: map[string]bool{}, next: map[string]int{}}
}

func (n *Namer) Stem(index int, rawURL string) string {
	numbered := fmt.Sprintf("%03d", index)
	if !n.keep {
		return numbered
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return n.unique(numbered)
	}
	base := path.Base(u.Path)
	base = SanitizeName(strings.TrimSuffix(base, path.Ext(base)))
	if base == "" {
		return n.unique(numbered)
	}
	return n.unique(base)
}

// unique returns base, or base-2, base-3, ... when that stem is taken. A
// generated suffix is checked like any other name, so it never lands on a
// file whose real name is already "photo-2".
func (n *Namer) unique(base string) string {
	key := strings.ToLower(base)
	stem := base
	for c := max(n.next[key], 2); n.taken[strings.ToLower(stem)]; c++ {
		stem = fmt.Sprintf("%s-%d", base, c)
		n.next[key] = c + 1
	}
	n.taken[strings.ToLower(stem)] = true
	return stem
}

// Exists reports whether a finished file with this stem is in dir. A .part
// file is an interrupted download and does not count.
func Exists(dir, stem string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".part") {
			continue
		}
		if e.IsDir() {
			continue
		}
		if strings.TrimSuffix(name, filepath.Ext(name)) == stem {
			return true
		}
	}
	return false
}

// ExtOf picks a file extension: a known image extension from the URL, else
// the Content-Type, else the type sniffed from body, else ".jpg".
func ExtOf(rawURL, contentType string, body []byte) string {
	if u, err := url.Parse(rawURL); err == nil {
		if ext := strings.ToLower(path.Ext(u.Path)); urlExt[ext] {
			return ext
		}
	}
	if ext, ok := extOfMIME(contentType); ok {
		return ext
	}
	if len(body) > 0 {
		if ext, ok := extOfMIME(http.DetectContentType(body)); ok {
			return ext
		}
	}
	return ".jpg"
}

func extOfMIME(contentType string) (string, bool) {
	mime := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	ext, ok := contentTypeExt[mime]
	return ext, ok
}

// Save writes to a .part file first and renames it, so an interrupted
// download is never mistaken for a finished one on the next run.
func Save(dir, stem, rawURL, contentType string, body []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := stem + ExtOf(rawURL, contentType, body)
	dest := filepath.Join(dir, name)
	tmp := dest + ".part"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

func RemoveParts(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
