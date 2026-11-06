package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"runtime"
)

// Status colors.  Kept to one saturated dot per state so it reads at a
// glance in both light and dark menu bars.
var (
	colorOnline     = color.NRGBA{R: 0x34, G: 0xC7, B: 0x59, A: 0xFF} // green
	colorPaused     = color.NRGBA{R: 0xFF, G: 0x9F, B: 0x0A, A: 0xFF} // amber
	colorConnecting = color.NRGBA{R: 0x8E, G: 0x8E, B: 0x93, A: 0xFF} // gray
	colorOffline    = color.NRGBA{R: 0xFF, G: 0x3B, B: 0x30, A: 0xFF} // red
)

// dotPNG renders a filled, anti-aliased circle at the requested size.
// Generated at runtime so the repo carries no binary assets and the
// icon always matches the current state.
func dotPNG(size int, col color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx := float64(size) / 2
	cy := float64(size) / 2
	radius := float64(size) * 0.40
	const ss = 4 // 4x4 supersampling for smooth edges
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			covered := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					dx, dy := px-cx, py-cy
					if dx*dx+dy*dy <= radius*radius {
						covered++
					}
				}
			}
			if covered == 0 {
				continue
			}
			alpha := uint8(255 * covered / (ss * ss))
			img.SetNRGBA(x, y, color.NRGBA{R: col.R, G: col.G, B: col.B, A: alpha})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// encodeICO wraps one or more images into a Windows .ico.  systray on
// Windows keeps the icon in a temp file and LoadImage wants ICO, so a
// plain PNG is not enough there (darwin accepts PNG via NSImage).
func encodeICO(images []image.Image) []byte {
	const iconDirSize = 6
	const iconEntrySize = 16

	var payload bytes.Buffer
	offsets := make([]int, len(images))
	sizes := make([]int, len(images))
	for i, img := range images {
		offsets[i] = iconDirSize + iconEntrySize*len(images) + payload.Len()
		sizes[i] = payload.Len()
		payload.Write(encodeDIB(img))
		sizes[i] = payload.Len() - sizes[i]
	}

	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	_ = binary.Write(&out, binary.LittleEndian, uint16(1)) // type: icon
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(images)))
	for i, img := range images {
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		if w > 255 {
			w = 0
		}
		if h > 255 {
			h = 0
		}
		out.WriteByte(byte(w))
		out.WriteByte(byte(h))
		out.WriteByte(0) // palette size
		out.WriteByte(0)
		_ = binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
		_ = binary.Write(&out, binary.LittleEndian, uint16(32)) // bpp
		_ = binary.Write(&out, binary.LittleEndian, uint32(sizes[i]))
		_ = binary.Write(&out, binary.LittleEndian, uint32(offsets[i]))
	}
	out.Write(payload.Bytes())
	return out.Bytes()
}

// encodeDIB builds the BITMAPINFOHEADER + XOR bitmap + AND mask layout
// an ICO entry expects.  Rows are bottom-up BGRA.
func encodeDIB(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	maskStride := (w + 31) / 32 * 4
	var buf bytes.Buffer

	_ = binary.Write(&buf, binary.LittleEndian, uint32(40))                 // header size
	_ = binary.Write(&buf, binary.LittleEndian, int32(w))                   // width
	_ = binary.Write(&buf, binary.LittleEndian, int32(h*2))                 // height (XOR+AND)
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))                  // planes
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32))                 // bpp
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))                  // compression
	_ = binary.Write(&buf, binary.LittleEndian, uint32(w*h*4+maskStride*h)) // image size
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))                   // x ppm
	_ = binary.Write(&buf, binary.LittleEndian, int32(0))                   // y ppm
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))                  // colors used
	_ = binary.Write(&buf, binary.LittleEndian, uint32(0))                  // colors important

	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			r, g, bb, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			buf.WriteByte(byte(bb >> 8))
			buf.WriteByte(byte(g >> 8))
			buf.WriteByte(byte(r >> 8))
			buf.WriteByte(byte(a >> 8))
		}
	}
	// AND mask: 0 means "use the alpha channel".
	buf.Write(make([]byte, maskStride*h))
	return buf.Bytes()
}

// trayIcon picks the platform-specific bytes for the current state.
func trayIcon(state string, allowControl bool) []byte {
	col := colorConnecting
	switch state {
	case "online":
		col = colorOnline
	case "paused":
		col = colorPaused
	case "offline":
		col = colorOffline
	}
	if state == "online" && !allowControl {
		col = colorPaused
	}
	if runtime.GOOS == "windows" {
		return encodeICO([]image.Image{
			dotImage(16, col),
			dotImage(32, col),
		})
	}
	return dotPNG(22, col)
}

func dotImage(size int, col color.NRGBA) image.Image {
	// Decode is deliberately avoided: build the image directly.
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx := float64(size) / 2
	cy := float64(size) / 2
	radius := float64(size) * 0.40
	const ss = 4
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			covered := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					dx, dy := px-cx, py-cy
					if dx*dx+dy*dy <= radius*radius {
						covered++
					}
				}
			}
			if covered == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{R: col.R, G: col.G, B: col.B, A: uint8(255 * covered / (ss * ss))})
		}
	}
	return img
}
