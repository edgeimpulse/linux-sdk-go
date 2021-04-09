// Command eimcollect uploads measurements to EdgeImpulse for processing into a model.
//
// Example:
//
//	# Upload file payload.json with given credentials, as training data, without additional labels.
//	eimcollect your_api_key your_hmac_key payload.json
//
//	# Upload to explicit URL, with label eimcollect, as testing data
//	eimcollect -baseurl https://ingestion.edgeimpulse.com -label eimcollect -category testing your_api_key your_hmac_key payload.json
//
// Payload.json must be in the format specified in package ingest.
package main

import (
	"context"
	"flag"
	"log"
	"math"
	"os"

	"github.com/edgeimpulse/linux-sdk-go/ingest"
)

var (
	baseURL            = flag.String("baseurl", "", "base URL to which payloads are sent")
	disallowDuplicates = flag.Bool("disallow-duplicates", false, "disallow duplicates")
	label              = flag.String("label", "", "label for data")
	category           = flag.String("category", "training", "type of data: split, training or testing")
)

func usage() {
	log.Println("usage: eimcollect [-baseurl https://...] [-label label] [-allow-duplicates] [-category split|training|testing] apikey hmackey payload.json")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()
	if len(args) != 2 {
		usage()
	}

	apiKey := args[0]
	hmacKey := args[1]
	opts := ingest.UploadOpts{
		Label:              *label,
		DisallowDuplicates: *disallowDuplicates,
	}
	c, err := ingest.NewCollector(apiKey, hmacKey)
	if err != nil {
		log.Fatalf("new collector: %v", err)
	}
	if *baseURL != "" {
		c.IngestionBaseURL = *baseURL
	}

	var values [][]float64
	for i := 0; i <= 200; i++ {
		ix := float64(i)
		frame := []float64{
			math.Sin(ix*0.1) * 10,
			math.Cos(ix*0.1) * 10,
			(math.Sin(ix*0.1) + math.Cos(ix*0.1)) * 10,
		}

		values = append(values, frame)
	}

	payload := ingest.CollectPayload{
		DeviceName: "00:00:00:00:00:00", // set this to a **globally unique** identifier
		DeviceType: "LINUX_GO_EXAMPLE",
		IntervalMS: 10,
		Sensors: []ingest.Sensor{
			{Name: "accX", Units: "m/s2"},
			{Name: "accY", Units: "m/s2"},
			{Name: "accZ", Units: "m/s2"},
		},
		Values: values,
	}

	sampleName, err := c.Upload(context.Background(), "linux01", *category, payload, &opts)
	if err != nil {
		log.Fatalf("upload: %v", err)
	}
	log.Printf("uploaded: sample name: %s", sampleName)
}
