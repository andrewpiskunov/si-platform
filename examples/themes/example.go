// Package themeexample builds the original, reproducible custom-theme example.
package themeexample

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"time"
)

//go:embed theme.json
var manifest []byte

//go:embed guide.html
var guide []byte

// Guide returns the standalone Russian authoring guide for static publication.
func Guide() []byte { return append([]byte(nil), guide...) }

// Build uses fixed entry order, permissions and timestamps. The PNG is drawn
// solely from geometry below; neither source assets nor external services are used.
func Build() ([]byte, error) {
	var illustration bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&illustration, compass()); err != nil {
		return nil, err
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	entries := []struct {
		name string
		data []byte
	}{{"theme.json", manifest}, {"assets/compass.png", illustration.Bytes()}}
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
		header.SetMode(0644)
		target, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err = target.Write(entry.data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}

// compass is an original 128 px geometric mark with a transparent surround.
func compass() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	blue := color.NRGBA{142, 169, 194, 255}
	gold := color.NRGBA{255, 224, 138, 255}
	light := color.NRGBA{245, 248, 255, 255}
	dark := color.NRGBA{16, 35, 52, 255}
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			dx, dy := 2*x-127, 2*y-127
			ax, ay := abs(dx), abs(dy)
			distance := dx*dx + dy*dy
			var pixel color.NRGBA
			if distance >= 99*99 && distance <= 105*105 {
				pixel = blue
			}
			if ax+ay <= 72 && ax+ay >= 54 {
				pixel = blue
			}
			// Four elongated diamonds form the cardinal points of the compass.
			if 4*ax+ay <= 113 || ax+4*ay <= 113 {
				pixel = gold
				if dx+dy > 0 {
					pixel = light
				}
			}
			if distance <= 15*15 {
				pixel = dark
			}
			if distance <= 7*7 {
				pixel = gold
			}
			img.SetNRGBA(x, y, pixel)
		}
	}
	return img
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
