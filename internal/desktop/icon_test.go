package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"runtime"
	"testing"
)

func TestDotPNGIsValidPNG(t *testing.T) {
	data := dotPNG(22, colorOnline)
	if len(data) == 0 {
		t.Fatal("dotPNG returned no bytes")
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("not a PNG")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png decode: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 22 || b.Dy() != 22 {
		t.Fatalf("bad size: %v", b)
	}
	// Center must be the requested color with full alpha.
	r, g, b, a := img.At(11, 11).RGBA()
	if a == 0 {
		t.Fatal("center pixel is transparent")
	}
	if g < r || g < b {
		t.Fatalf("center pixel is not green-ish: r=%d g=%d b=%d", r, g, b)
	}
}

func TestTrayIconPlatformFormat(t *testing.T) {
	data := trayIcon("online", true)
	if len(data) == 0 {
		t.Fatal("empty tray icon")
	}
	if runtime.GOOS == "windows" {
		if len(data) < 6 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
			t.Fatal("windows icon is not ICO")
		}
		return
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG")) {
		t.Fatal("darwin icon is not PNG")
	}
}

func TestEncodeICOHeader(t *testing.T) {
	ico := encodeICO([]image.Image{
		dotImage(16, colorOnline),
		dotImage(32, colorOnline),
	})
	if len(ico) < 6 {
		t.Fatalf("short ICO: %d bytes", len(ico))
	}
	if got := binary.LittleEndian.Uint16(ico[2:4]); got != 1 {
		t.Fatalf("icon type = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(ico[4:6]); got != 2 {
		t.Fatalf("icon count = %d, want 2", got)
	}
	// Each directory entry must point inside the payload.
	for i := 0; i < 2; i++ {
		entry := ico[6+i*16 : 6+(i+1)*16]
		size := binary.LittleEndian.Uint32(entry[8:12])
		offset := binary.LittleEndian.Uint32(entry[12:16])
		if int(offset)+int(size) > len(ico) || size == 0 {
			t.Fatalf("entry %d out of bounds: size=%d offset=%d total=%d", i, size, offset, len(ico))
		}
	}
}
