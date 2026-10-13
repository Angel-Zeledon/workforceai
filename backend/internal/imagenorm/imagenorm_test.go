package imagenorm_test

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"aiworkforce/backend/internal/imagenorm"
)

func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	return img
}

func jpegWithExif(t *testing.T, w, h int, secret string) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, photo(w, h), nil); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	payload := append([]byte("Exif\x00\x00"), []byte(secret)...)
	seg := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)
	out := append([]byte{}, raw[:2]...) // SOI
	out = append(out, seg...)
	return append(out, raw[2:]...)
}

func TestNormalizeDownscalesAndStripsMetadata(t *testing.T) {
	in := jpegWithExif(t, 3200, 1600, "GPS 40.4168N 3.7038W secret-owner")
	if !bytes.Contains(in, []byte("secret-owner")) {
		t.Fatal("fixture must carry the metadata")
	}
	out, err := imagenorm.Normalize(in, imagenorm.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Width != 1568 || out.Height != 784 || out.Mime != "image/jpeg" {
		t.Fatalf("unexpected output %dx%d %s", out.Width, out.Height, out.Mime)
	}
	if bytes.Contains(out.Data, []byte("secret-owner")) || bytes.Contains(out.Data, []byte("Exif")) {
		t.Fatal("metadata survived the re-encode")
	}
	if imagenorm.Sniff(out.Data) != "image/jpeg" {
		t.Fatal("output is not a JPEG")
	}
}

func TestTransparencyKeepsPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	img.Set(1, 1, color.NRGBA{255, 0, 0, 128})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	out, err := imagenorm.Normalize(b.Bytes(), imagenorm.Options{})
	if err != nil || out.Mime != "image/png" || out.Width != 40 {
		t.Fatalf("%+v %v", out, err)
	}
}

// pngHeader is a PNG whose IHDR claims w x h (no pixel data: a bomb's header).
func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestRefusesBombsAndJunk(t *testing.T) {
	if _, err := imagenorm.Normalize(pngHeader(30000, 30000), imagenorm.Options{}); imagenorm.CodeOf(err) != imagenorm.CodeTooLarge {
		t.Fatalf("pixel bomb: %v", err)
	}
	if _, err := imagenorm.Normalize(pngHeader(9000, 9000), imagenorm.Options{}); imagenorm.CodeOf(err) != imagenorm.CodeTooLarge {
		t.Fatalf("81 MP must be refused before decoding: %v", err)
	}
	if _, err := imagenorm.Normalize(pngHeader(100, 100), imagenorm.Options{}); imagenorm.CodeOf(err) != imagenorm.CodeInvalid {
		t.Fatalf("header without pixels: %v", err)
	}
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 40)...)
	if _, err := imagenorm.Normalize(webp, imagenorm.Options{}); imagenorm.CodeOf(err) != imagenorm.CodeUnsupported {
		t.Fatalf("webp: %v", err)
	}
	if _, err := imagenorm.Normalize([]byte("not an image"), imagenorm.Options{}); imagenorm.CodeOf(err) != imagenorm.CodeUnsupported {
		t.Fatalf("junk: %v", err)
	}
	good := jpegWithExif(t, 64, 64, "x")
	if _, err := imagenorm.Normalize(good[:len(good)/2], imagenorm.Options{}); imagenorm.CodeOf(err) != imagenorm.CodeInvalid {
		t.Fatalf("truncated: %v", err)
	}
}

func TestOutputByteCap(t *testing.T) {
	// Noise does not compress: a tiny cap forces downscaling, an impossible one fails cleanly.
	if _, err := imagenorm.Normalize(jpegWithExif(t, 800, 800, "x"), imagenorm.Options{MaxBytes: 64}); imagenorm.CodeOf(err) != imagenorm.CodeTooLarge {
		t.Fatalf("byte cap: %v", err)
	}
	out, err := imagenorm.Normalize(jpegWithExif(t, 1200, 1200, "x"), imagenorm.Options{MaxBytes: 60 << 10})
	if err != nil || len(out.Data) > 60<<10 || out.Width >= 1200 {
		t.Fatalf("expected a smaller image: %d bytes %dx%d %v", len(out.Data), out.Width, out.Height, err)
	}
}
