// Command eimimage launches a model classification process, starts recording
// images from a camera (eg webcam), and classifies the image, printing the
// results.
//
// Examples:
//
//	# List available devices and quit.
//	eimimage -listdevices
//
//	# Record using default settings.
//	eimimage ../../models/linux-x86/jan-vs-niet-jan.eim
//
//	# Record using ffmpeg as recorder, with explicit device, every 250ms.
//	eimimage -recorder ffmpeg -device /dev/video0 -verbose -interval 250ms ../../models/linux-x86/jan-vs-niet-jan.eim
//
//	# Record using imagesnap. NOTE: on macOS, imagesnap is the default recorder.
//	eimimage -recorder imagesnap -device 'FaceTime HD Camera (Built-in)' -verbose -interval 250ms ../../models/mac/jan-vs-niet-jan.eim
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	edgeimpulse "github.com/edgeimpulse/linux-sdk-go"
	"github.com/edgeimpulse/linux-sdk-go/image"
	"github.com/edgeimpulse/linux-sdk-go/image/ffmpeg"
	"github.com/edgeimpulse/linux-sdk-go/image/gstreamer"
	"github.com/edgeimpulse/linux-sdk-go/image/imagesnap"
)

var (
	listDevices  bool
	recorderType string
	deviceID     string
	interval     time.Duration
	verbose      bool
	traceDir     string
)

func init() {
	if runtime.GOOS == "darwin" {
		recorderType = "imagesnap"
	} else {
		recorderType = "gstreamer"
	}

	flag.BoolVar(&listDevices, "listdevices", false, "if set, lists devices and exits")
	flag.StringVar(&recorderType, "recorder", recorderType, "type of recorder to use, imagesnap on macOS; gstreamer or ffmpeg on linux")
	flag.StringVar(&deviceID, "device", "", "device ID to use, by default, the first device returned when listing devices")
	flag.DurationVar(&interval, "interval", 250*time.Millisecond, "how often to take an image and classify it")
	flag.BoolVar(&verbose, "verbose", false, "print verbose output")
	flag.StringVar(&traceDir, "tracedir", "", "if set, store the images and parsed classify data to the named directory")
}

func usage() {
	log.Println("usage: eimimage [flags] model")
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
	var listFn func() ([]image.Device, error)
	switch recorderType {
	case "imagesnap":
		listFn = imagesnap.ListDevices
	case "gstreamer":
		listFn = gstreamer.ListDevices
	case "ffmpeg":
		listFn = ffmpeg.ListDevices
	default:
		log.Fatalf("unknown recorder type %q", recorderType)
	}

	if listDevices {
		devs, err := listFn()
		if err != nil {
			log.Fatalf("listing devices: %v", err)
		}
		for _, dev := range devs {
			caps := ""
			if len(dev.Caps) > 0 {
				l := []string{}
				for _, c := range dev.Caps {
					l = append(l, fmt.Sprintf("%dx%d@%dfps", c.Width, c.Height, c.Framerate))
				}
				caps = fmt.Sprintf(" (caps: %s)", strings.Join(l, " "))
			}
			fmt.Printf("%s: %s%s\n", dev.ID, dev.Name, caps)
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

	var recorder image.Recorder
	switch recorderType {
	case "gstreamer":
		var err error
		recorderOpts := gstreamer.RecorderOpts{
			Verbose:  verbose,
			Interval: interval,
			DeviceID: deviceID,
		}
		recorder, err = gstreamer.NewRecorder(recorderOpts)
		if err != nil {
			log.Printf("new gstreamer recorder: %v", err)
			return 1
		}
	case "ffmpeg":
		var err error
		recorderOpts := ffmpeg.RecorderOpts{
			Verbose:  verbose,
			Interval: interval,
			DeviceID: deviceID,
		}
		recorder, err = ffmpeg.NewRecorder(recorderOpts)
		if err != nil {
			log.Printf("new ffmpeg recorder: %v", err)
			return 1
		}
	case "imagesnap":
		var err error
		recorderOpts := imagesnap.RecorderOpts{
			Verbose:  verbose,
			Interval: interval,
			DeviceID: deviceID,
		}
		recorder, err = imagesnap.NewRecorder(recorderOpts)
		if err != nil {
			log.Printf("new imagesnap recorder: %v", err)
			return 1
		}
	default:
		log.Fatalf("bad recorder type %q", recorderType)
	}
	defer recorder.Close()

	opts := &image.ClassifierOpts{
		Verbose:  verbose,
		TraceDir: traceDir,
	}
	cl, err := image.NewClassifier(runner, recorder, opts)
	if err != nil {
		log.Printf("new image classifier: %v", err)
		return 1
	}
	defer cl.Close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-signals:
			return 1
		case ev, ok := <-cl.Events:
			if !ok {
				log.Printf("no more events")
				return 1
			}
			if ev.Err != nil {
				log.Printf("%s", ev.Err)
			} else {
				fmt.Printf("%v\n", ev.RunnerClassifyResponse)
			}
		}
	}
}
