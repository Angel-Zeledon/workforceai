// Package imagenorm prepares an image for a vision model: it decodes it with
// the memory-safe Go standard library (PNG, JPEG, GIF first frame), refuses
// decompression bombs from the header (pixel and side caps checked BEFORE the
// pixels are decoded), downscales it to a maximum long side and re-encodes it.
// Re-encoding drops every piece of metadata (EXIF with GPS position, comments,
// ICC profiles, XMP): only pixels leave the backend.
//
// It is the fallback used when the sandbox render capability is not configured
// (see docs/architecture/agent-vision-pdf.md); formats the standard library
// cannot decode (WebP) need the sandbox.
package imagenorm

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
)

// Stable failure codes.
const (
	CodeUnsupported = "image_unsupported" // not PNG/JPEG/GIF (WebP needs the sandbox)
	CodeTooLarge    = "image_too_large"   // pixel/side caps or the output size cap
	CodeInvalid     = "image_invalid"     // corrupt or truncated
)

// Error carries a stable code.
type Error struct{ Code, Msg string }

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

// CodeOf returns the stable code of err ("" when none).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func fail(code, format string, a ...any) *Error { return &Error{code, fmt.Sprintf(format, a...)} }

// Options bound one normalization. Zero values take the defaults.
type Options struct {
	MaxSide   int   // long side of the output in pixels (default 1568)
	MaxBytes  int   // size of the output (default 5 MB)
	MaxPixels int64 // pixels of the INPUT (default 40 MP)
	MaxInput  int   // bytes of the input (default 25 MB)
}

// Defaults.
const (
	DefaultMaxSide   = 1568 // what the large vision models use without tiling
	DefaultMaxBytes  = 5 << 20
	DefaultMaxPixels = 40_000_000
	DefaultMaxInput  = 25 << 20
	maxSideIn        = 20000
)

func (o Options) withDefaults() Options {
	if o.MaxSide <= 0 {
		o.MaxSide = DefaultMaxSide
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.MaxPixels <= 0 {
		o.MaxPixels = DefaultMaxPixels
	}
	if o.MaxInput <= 0 {
		o.MaxInput = DefaultMaxInput
	}
	return o
}

// Image is a normalized image.
type Image struct {
	Mime          string
	Data          []byte
	Width, Height int
}

// Sniff returns the mime type of known image bytes ("" when unknown).
func Sniff(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return "image/gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// Normalize decodes, bounds, downscales and re-encodes an image.
func Normalize(data []byte, opt Options) (img Image, err error) {
	opt = opt.withDefaults()
	defer func() { // decoders do not panic on bad input, but a bug must never take the server down
		if p := recover(); p != nil {
			img, err = Image{}, fail(CodeInvalid, "the image could not be decoded")
		}
	}()
	if len(data) > opt.MaxInput {
		return Image{}, fail(CodeTooLarge, "the image is larger than %d MB", opt.MaxInput>>20)
	}
	mime := Sniff(data)
	var cfg image.Config
	switch mime {
	case "image/png":
		cfg, err = png.DecodeConfig(bytes.NewReader(data))
	case "image/jpeg":
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case "image/gif":
		cfg, err = gif.DecodeConfig(bytes.NewReader(data))
	default:
		return Image{}, fail(CodeUnsupported, "only PNG, JPEG and GIF images can be prepared here")
	}
	if err != nil {
		return Image{}, fail(CodeInvalid, "the image header could not be read")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxSideIn || cfg.Height > maxSideIn ||
		int64(cfg.Width)*int64(cfg.Height) > opt.MaxPixels {
		return Image{}, fail(CodeTooLarge, "the image is %dx%d pixels: too large to prepare", cfg.Width, cfg.Height)
	}
	var src image.Image
	switch mime {
	case "image/png":
		src, err = png.Decode(bytes.NewReader(data))
	case "image/jpeg":
		src, err = jpeg.Decode(bytes.NewReader(data))
	case "image/gif":
		src, err = gif.Decode(bytes.NewReader(data)) // first frame only
	}
	if err != nil {
		return Image{}, fail(CodeInvalid, "the image could not be decoded")
	}
	side := opt.MaxSide
	for attempt := 0; attempt < 6; attempt++ {
		dst := Downscale(src, side)
		out, mt, err := encode(dst)
		if err != nil {
			return Image{}, fail(CodeInvalid, "the image could not be encoded")
		}
		if len(out) <= opt.MaxBytes {
			b := dst.Bounds()
			return Image{Mime: mt, Data: out, Width: b.Dx(), Height: b.Dy()}, nil
		}
		side = side * 2 / 3
	}
	return Image{}, fail(CodeTooLarge, "the image stays over %d KB after downscaling", opt.MaxBytes>>10)
}

// encode writes JPEG (quality 85) for opaque images and PNG when transparency matters.
func encode(img image.Image) ([]byte, string, error) {
	var b bytes.Buffer
	if opaque(img) {
		err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85})
		return b.Bytes(), "image/jpeg", err
	}
	err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, img)
	return b.Bytes(), "image/png", err
}

func opaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

// Downscale returns img with its long side at most maxSide (area averaging),
// always as a fresh RGBA/NRGBA copy (so nothing but pixels survives).
func Downscale(img image.Image, maxSide int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	nw, nh := w, h
	if long := max(w, h); long > maxSide && maxSide > 0 {
		nw = max(1, w*maxSide/long)
		nh = max(1, h*maxSide/long)
	}
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	if nw == w && nh == h {
		return src
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := y*h/nh, max((y+1)*h/nh, y*h/nh+1)
		for x := 0; x < nw; x++ {
			x0, x1 := x*w/nw, max((x+1)*w/nw, x*w/nw+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				off := sy*src.Stride + x0*4
				for sx := x0; sx < x1; sx++ {
					p := src.Pix[off : off+4 : off+4]
					r, g, bl, a = r+uint64(p[0]), g+uint64(p[1]), bl+uint64(p[2]), a+uint64(p[3])
					n++
					off += 4
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{uint8(r / n), uint8(g / n), uint8(bl / n), uint8(a / n)})
		}
	}
	return dst
}
