package handler

import (
	"bytes"
	"image"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder for image.Decode
	"io"
)

// Java's S3UploadService caps the long edge at 1920 and re-encodes as JPEG at
// 0.82. A phone photo is 3-8MB; this brings it under ~300KB with no visible
// loss, which is 80-90% off both the upload time and the stored bytes.
const (
	maxImageDimension = 1920
	jpegQuality       = 82
)

// compressImage re-encodes an uploaded image as JPEG, scaling it down if either
// side exceeds maxImageDimension.
//
// Compression happens here rather than in the browser because the browser is
// not the only caller: anything can POST to this endpoint, so a client-side
// resize is a courtesy, never a guarantee. Doing it server-side means the cap
// actually holds.
//
// Returns ok=false when the bytes are not a format we can re-encode (WebP and
// GIF have no stdlib encoder); the caller then stores the original, which is
// what Java does when ImageIO cannot read a file.
func compressImage(r io.Reader) (out []byte, contentType, ext string, ok bool) {
	src, _, err := image.Decode(r)
	if err != nil {
		return nil, "", "", false
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > maxImageDimension || h > maxImageDimension {
		src = scaleDown(src, w, h)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flatten(src), &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", "", false
	}
	return buf.Bytes(), "image/jpeg", ".jpg", true
}

// scaleDown does a box-average resample. Nearest-neighbour is one line shorter
// but aliases badly on photographs, which is the entire input here.
//
// ponytail: hand-rolled rather than pulling in golang.org/x/image/draw for one
// call. Swap in draw.CatmullRom if the quality ever matters more than the
// dependency.
func scaleDown(src image.Image, w, h int) image.Image {
	scale := float64(maxImageDimension) / float64(max(w, h))
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	bx, by := float64(w)/float64(nw), float64(h)/float64(nh)
	b := src.Bounds()
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			// Average the source box this destination pixel covers.
			x0, y0 := int(float64(x)*bx), int(float64(y)*by)
			x1, y1 := int(float64(x+1)*bx), int(float64(y+1)*by)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			var rs, gs, bs, as, n uint64
			for sy := y0; sy < y1 && sy < b.Dy(); sy++ {
				for sx := x0; sx < x1 && sx < b.Dx(); sx++ {
					r, g, bl, a := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					rs += uint64(r)
					gs += uint64(g)
					bs += uint64(bl)
					as += uint64(a)
					n++
				}
			}
			if n == 0 {
				continue
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = uint8(rs / n >> 8)
			dst.Pix[i+1] = uint8(gs / n >> 8)
			dst.Pix[i+2] = uint8(bs / n >> 8)
			dst.Pix[i+3] = uint8(as / n >> 8)
		}
	}
	return dst
}

// flatten composites onto white. JPEG has no alpha channel, so a transparent
// PNG would otherwise encode its transparent areas as black.
func flatten(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := src.At(x, y).RGBA()
			// Source-over white: c*alpha + 255*(1-alpha), in 16-bit space.
			inv := 0xffff - a
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = uint8((r + inv) >> 8)
			dst.Pix[i+1] = uint8((g + inv) >> 8)
			dst.Pix[i+2] = uint8((bl + inv) >> 8)
			dst.Pix[i+3] = 0xff
		}
	}
	return dst
}
