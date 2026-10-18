package main

import (
	"bytes"
	"image/png"
	"testing"
)

func TestPackagedIcons(t *testing.T) {
	for _, icon := range []struct {
		name string
		data []byte
		size int
	}{{"app", appIconPNG, 1024}, {"tray", trayIconPNG, 64}} {
		t.Run(icon.name, func(t *testing.T) {
			img, err := png.Decode(bytes.NewReader(icon.data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != icon.size || img.Bounds().Dy() != icon.size {
				t.Fatalf("unexpected dimensions: %v", img.Bounds())
			}
			visible := 0
			shades := map[uint64]bool{}
			for y := 0; y < icon.size; y++ {
				for x := 0; x < icon.size; x++ {
					r, g, b, a := img.At(x, y).RGBA()
					if a == 0 {
						continue
					}
					if x == 0 || y == 0 || x == icon.size-1 || y == icon.size-1 {
						t.Fatal("icon is clipped at the image boundary")
					}
					visible++
					if a == 65535 {
						shades[uint64(r)<<32|uint64(g)<<16|uint64(b)] = true
					}
				}
			}
			if visible < icon.size*icon.size/5 || len(shades) < 8 {
				t.Fatalf("missing icon detail: visible=%d shades=%d", visible, len(shades))
			}
		})
	}
	if !bytes.Equal(platformApplicationIcon(), appIconPNG) {
		t.Fatal("runtime icon must use the same fixed artwork as packaged icons")
	}
}
