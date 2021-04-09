// reads a classification request, writes png image. expects a 96x96 image.
// eimimagereq < request.json > out.png
package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
)

var data struct {
	ID       int
	Classify []uint32
}

func main() {
	log.SetFlags(0)

	if err := json.NewDecoder(os.Stdin).Decode(&data); err != nil {
		log.Fatalf("decode json: %v", err)
	}

	if len(data.Classify) != 96*96 {
		log.Fatalf("unexpected size (%d values)", len(data.Classify))
	}

	img := image.NewNRGBA(image.Rect(0, 0, 96, 96))
	i := 0
	for y := 0; y < 96; y++ {
		for x := 0; x < 96; x++ {
			v := data.Classify[i]
			i++
			r := uint8((v >> 16) & 0xff)
			g := uint8((v >> 8) & 0xff)
			b := uint8((v >> 0) & 0xff)
			// log.Printf("%d,%d %d: %x %x %x %x", x, y, i, r, g, b, v)
			c := color.RGBA{r, g, b, 0xff}
			img.Set(x, y, c)
		}
	}
	if err := png.Encode(os.Stdout, img); err != nil {
		log.Fatalf("writing png: %v", err)
	}
}
