// Package imagesnap implements an image recorder with the imagesnap command
// for macOS.
package imagesnap

import (
	"context"
	"fmt"
	"image/jpeg"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	edgeimpulse "github.com/edgeimpulse/linux-sdk-go"
	"github.com/edgeimpulse/linux-sdk-go/image"

	"github.com/fsnotify/fsnotify"
)

// ListDevices returns all image capturing devices available to imagesnap.
// ListDevices returns an error if no devices are available.
func ListDevices() ([]image.Device, error) {
	cmd := exec.Command("imagesnap", "-l")
	buf, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing devices with imagesnap -l: %v", err)
	}
	return parseDevices(string(buf))
}

func parseDevices(s string) ([]image.Device, error) {
	devs := []image.Device{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "=> ") {
			// Newer format, example: "=> FaceTime HD Camera (Built-in)"
			name := line[len("=> "):]
			devs = append(devs, image.Device{Name: name, ID: name})
		} else if strings.HasPrefix(line, "<") {
			// Older format, example: "<AVCaptureDALDevice: 0x7fa2c7852fd0 [FaceTime HD Camera (Built-in)][0x8020000005ac8514]>"
			t := strings.Split(line, "[")
			if len(t) < 2 {
				continue
			}
			name := strings.Split(t[1], "]")[0]
			devs = append(devs, image.Device{Name: name, ID: name})
		} else {
			continue
		}
	}
	if len(devs) == 0 {
		return nil, fmt.Errorf("no devices available")
	}
	return devs, nil
}

// RecorderOpts has options for a new imagesnap recorder.
type RecorderOpts struct {
	Verbose  bool
	Interval time.Duration // How often to record an image.
	DeviceID string        // As returned by ListDevices. If empty, NewRecorder will use the first device returned by ListDevices.
}

// Recorder records images by starting imagesnap and configuring it to write images to temporary storage.
type Recorder struct {
	opts        RecorderOpts
	imageEvents chan image.Event
	tempDir     string
	cancel      context.CancelFunc
	watcher     *fsnotify.Watcher
}

// Check that Recorder implements interface Recorder.
var _ image.Recorder = (*Recorder)(nil)

// Events returns a channel on which Events can be received.
func (r *Recorder) Events() chan image.Event {
	return r.imageEvents
}

// NewRecorder creates a new recorder by starting imagesnap, making it write
// images to a temporary directory. These images are read and sent on the
// channel returned by Events.
//
// Callers must call Close to clean up.
func NewRecorder(opts RecorderOpts) (recorder *Recorder, rerr error) {
	r := &Recorder{}
	r.opts = opts

	if r.opts.DeviceID == "" {
		devs, err := ListDevices()
		if err != nil {
			return nil, fmt.Errorf("listing devices: %v", err)
		}
		r.opts.DeviceID = devs[0].ID
	}

	// Ensure cleanup in case of failure.
	defer func() {
		if rerr != nil {
			r.Close()
		}
	}()

	tempDir, err := edgeimpulse.TempDir()
	if err != nil {
		return nil, fmt.Errorf("making temp dir: %v", err)
	}
	r.tempDir = tempDir
	if r.opts.Verbose {
		log.Printf("imagesnap recorder, tempdir for images: %s", r.tempDir)
	}

	args := []string{
		"-d", r.opts.DeviceID,
		"-t", fmt.Sprintf("%.2f", r.opts.Interval.Seconds()),
	}

	if r.opts.Verbose {
		log.Printf("starting imagesnap with args %s", args)
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	cmd := exec.CommandContext(ctx, "imagesnap", args...)
	cmd.Dir = r.tempDir
	if r.opts.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting imagesnap: %v", err)
	}
	go cmd.Wait()

	r.imageEvents = make(chan image.Event)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("new file change watcher: %v", err)
	}
	r.watcher = watcher

	logf := func(format string, args ...interface{}) {
		if r.opts.Verbose {
			log.Printf(format, args...)
		}
	}

	go func() {
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if ev.Op != fsnotify.Create || !strings.HasSuffix(ev.Name, ".jpg") {
					continue
				}
				f, err := os.Open(ev.Name)
				if err != nil {
					logf("open written file %q: %v", ev.Name, err)
					continue
				}
				img, err := jpeg.Decode(f)
				f.Close()
				if err != nil {
					logf("decoding jpeg %q: %v (perhaps partially written?)", ev.Name, err)
					continue
				}
				if err := os.Remove(ev.Name); err != nil && r.opts.Verbose {
					log.Printf("removing image %s: %v", ev.Name, err)
				}
				select {
				case r.imageEvents <- image.Event{Image: img}:
				default:
					if r.opts.Verbose {
						log.Printf("dropping image, classifier still busy")
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				r.imageEvents <- image.Event{Err: fmt.Errorf("watching for changes: %v", err)}
			}
		}
	}()

	if err := watcher.Add(r.tempDir); err != nil {
		return nil, fmt.Errorf("registering file change watcher for temp dir: %v", err)
	}

	return r, nil
}

// Close shuts down the recorder, stopping the imagesnap process and removing
// the temporary directory.
func (r *Recorder) Close() error {
	if r.cancel != nil {
		r.cancel()
	}
	if r.watcher != nil {
		r.watcher.Close()
	}
	if r.tempDir != "" {
		os.RemoveAll(r.tempDir)
	}
	return nil
}
