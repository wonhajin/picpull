package pick

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// DecodedSize reads the size from the image header without decoding pixels.
// AVIF and other formats Go cannot read report ok=false.
func DecodedSize(body []byte) (w, h int, ok bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}
