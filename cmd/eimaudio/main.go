// Command eimaudio launches an audio (microphone) model process, records audio
// from your microphone, and classifies the audio samples using the model.
//
// Examples:
//
//	# Classify audio samples with default settings.
//	eimaudio ../../custom-keywords.eim
//
//	# Classify audio, and apply a moving average filter with a history of 4.
//	eimaudio -maf 4 -verbose -interval 250ms ../../custom-keywords.eim
//
//	# List audio devices, to be used with the -device flag.
//	eimaudio -listdevices
//
//	# List audio devices, to be used with the -device flag.
//	eimaudio -device hw:0,0 ../../custom-keywords.eim
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	edgeimpulse "github.com/edgeimpulse/linux-sdk-go"
	"github.com/edgeimpulse/linux-sdk-go/audio"
	"github.com/edgeimpulse/linux-sdk-go/audio/audiocmd"
)

var (
	listDevices bool
	interval    time.Duration
	mafSize     int
	verbose     bool
	traceDir    string
	deviceID    string
)

func init() {
	flag.BoolVar(&listDevices, "listdevices", false, "if set, lists devices and exits")
	flag.DurationVar(&interval, "interval", 250*time.Millisecond, "classify audio every interval")
	flag.IntVar(&mafSize, "maf", 0, "apply moving-average-filter for all labels of the model of given size (only if >0)")
	flag.BoolVar(&verbose, "verbose", false, "print more logging")
	flag.StringVar(&traceDir, "tracedir", "", "if set, store the parsed classify data to the named directory")
	flag.StringVar(&deviceID, "device", "", "if set, device ID is used for microphone instead of the default microphone")
}

func usage() {
	log.Println("usage: eimaudio [flags] model")
	flag.PrintDefaults()
	os.Exit(2)
}

func main() {
	log.SetFlags(0)
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()
	os.Exit(main0(args))
}

func main0(args []string) int {
	if listDevices {
		devs, err := audiocmd.ListDevices()
		if err != nil {
			log.Fatalf("listing devices: %v", err)
		}
		for _, dev := range devs {
			log.Printf("%v: %v", dev.ID, dev.Name)
		}
		os.Exit(0)
	}

	if len(args) != 1 {
		usage()
	}
	ropts := &edgeimpulse.RunnerOpts{
		TraceDir: traceDir,
	}
	runner, err := edgeimpulse.NewRunnerProcess(args[0], ropts)
	if err != nil {
		log.Printf("new runner: %v", err)
		return 1
	}
	defer runner.Close()

	log.Printf("project %s\nmodel %s", runner.Project(), runner.ModelParameters())

	recOpts := &audiocmd.RecorderOpts{
		SampleRate:    int(runner.ModelParameters().Frequency),
		Channels:      1,
		AsRaw:         true,
		RecordProgram: "sox",
		Verbose:       verbose,
		DeviceID:      deviceID,
	}
	recorder, err := audiocmd.NewRecorder(recOpts)
	if err != nil {
		log.Printf("new recorder: %v", err)
		return 1
	}
	defer recorder.Close()

	copts := &audio.ClassifierOpts{
		Verbose: verbose,
	}
	ac, err := audio.NewClassifier(runner, recorder, interval, copts)
	if err != nil {
		log.Printf("new audio classifier: %v", err)
		return 1
	}
	defer ac.Close()

	var maf *edgeimpulse.MAF
	if mafSize > 0 {
		if verbose {
			log.Printf("applying moving average filter of size %d", mafSize)
		}
		maf, err = edgeimpulse.NewMAF(mafSize, runner.ModelParameters().Labels)
		if err != nil {
			log.Printf("new MAF: %v", err)
		}
	}

	// Handle signals, so cleanup of the runners temporary directory is done.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	// Keep reading classification events.
	for {
		select {
		case <-signals:
			return 1
		case ev, ok := <-ac.Events:
			if !ok {
				log.Printf("no more events")
				return 0
			}
			if ev.Err != nil {
				log.Printf("%s", ev.Err)
			} else {
				if maf != nil {
					r, err := maf.Update(ev.RunnerClassifyResponse.Result.Classification)
					if err != nil {
						log.Printf("update moving average filter: %v", err)
					}
					ev.RunnerClassifyResponse.Result.Classification = r
				}
				fmt.Printf("%s\n", ev.RunnerClassifyResponse)
			}
		}
	}
}
