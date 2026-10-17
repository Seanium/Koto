//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"image/png"
	"testing"
)

func TestNativeEmojiIcon(t *testing.T) {
	for _, size := range []int{32, 64, 128, 512} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			image, err := png.Decode(bytes.NewReader(nativeEmojiPNG(size)))
			if err != nil {
				t.Fatal(err)
			}
			if image.Bounds().Dx() != size || image.Bounds().Dy() != size {
				t.Fatalf("unexpected dimensions: %v", image.Bounds())
			}
			opaque := 0
			shades := map[uint32]bool{}
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					r, g, b, a := image.At(x, y).RGBA()
					if a == 0 {
						continue
					}
					if x == 0 || y == 0 || x == size-1 || y == size-1 {
						t.Fatal("emoji is clipped at the image boundary")
					}
					opaque++
					if a == 65535 {
						shades[r<<16|g<<8|b] = true
					}
				}
			}
			if opaque < size*size/5 || len(shades) < 8 {
				t.Fatalf("missing native emoji detail: nontransparent=%d shades=%d", opaque, len(shades))
			}
		})
	}
}
