// eimaudioreq >out.wav <runner-2-request.json
// requires a single channel, 16khz, 16bit width.
package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/youpy/go-wav"
)

var data struct {
	ID       int
	Classify []int16
}

func main() {
	log.SetFlags(0)

	if err := json.NewDecoder(os.Stdin).Decode(&data); err != nil {
		log.Fatalf("decode json: %v", err)
	}

	samples := make([]wav.Sample, len(data.Classify))
	for i, v := range data.Classify {
		samples[i] = wav.Sample{Values: [...]int{int(v), int(v)}}
	}
	if err := wav.NewWriter(os.Stdout, uint32(len(data.Classify)), 1, 16000, 16).WriteSamples(samples); err != nil {
		log.Fatalf("writing wav: %v", err)
	}
}
