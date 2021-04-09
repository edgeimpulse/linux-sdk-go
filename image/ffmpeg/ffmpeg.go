package ffmpeg

import (
	"context"
	"errors"
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

var errInstallHint = errors.New("executable not found, install with: sudo apt install -y ffmpeg v4l-utils")

// RecorderOpts has options for a new ffmpeg recorder.
type RecorderOpts struct {
	Verbose  bool
	Interval time.Duration // How often to record an image.
	DeviceID string        // As retrieved from ListDevices. If empty, NewRecorder will use the first device returned by ListDevices.
}

// Recorder is an image recorder using ffmpeg.
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

// ListDevices returns a list of devices that can be used for recording.
// ListDevices returns an error if no devices are available.
func ListDevices() ([]image.Device, error) {
	cmd := exec.Command("v4l2-ctl", "--list-devices")
	buf, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			err = errInstallHint
		}
		return nil, fmt.Errorf("listing devices using v4l2-ctl: %v", err)
	}
	var curDevice string
	devices := []image.Device{}
	for _, line := range strings.Split(string(buf), "\n") {
		if !strings.HasPrefix(line, "\t") {
			curDevice = strings.TrimSpace(line)
			continue
		}
		if curDevice == "" || strings.HasPrefix(curDevice, "bcm2835-") {
			continue
		}

		line = strings.TrimSpace(line)
		dev := image.Device{
			Name: fmt.Sprintf("%s (%s)", curDevice, line),
			ID:   line,
		}
		devices = append(devices, dev)
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("no devices available")
	}
	return devices, nil
}

// NewRecorder creates a new recorder using ffmpeg. Ffmpeg writes images to a
// temporary directory. These files are read and sent over the channel returned
// by Events.
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
		log.Printf("ffmpegrecorder, writing images to tempdir %s", r.tempDir)
	}

	args := []string{
		"-framerate", fmt.Sprintf("%d", int(time.Second/r.opts.Interval)),
		"-video_size", "640x480",
		"-c:v", "mjpeg",
		"-i", r.opts.DeviceID,
		"-f", "image2",
		"-c:v", "copy",
		"-bsf:v", "mjpeg2jpeg",
		"-qscale:v", "2",
		"test%d.jpg",
	}

	if r.opts.Verbose {
		log.Printf("starting ffmpeg with args %s", args)
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	ffmpeg := exec.CommandContext(ctx, "ffmpeg", args...)
	ffmpeg.Dir = r.tempDir
	if r.opts.Verbose {
		ffmpeg.Stdout = os.Stdout
		ffmpeg.Stderr = os.Stderr
	}
	if err := ffmpeg.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			err = errInstallHint
		}
		return nil, fmt.Errorf("starting command ffmpeg: %v", err)
	}
	go ffmpeg.Wait()

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
		var last time.Time
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if ev.Op != fsnotify.Write || !strings.HasSuffix(ev.Name, ".jpg") {
					continue
				}
				now := time.Now()
				if now.Sub(last) < r.opts.Interval*9/10 {
					if err := os.Remove(ev.Name); err != nil && r.opts.Verbose {
						log.Printf("removing skipped image %q: %v", ev.Name, err)
					}
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
					logf("decoding jpeg %q: %v (may be partially written)", ev.Name, err)
					continue
				}
				if err := os.Remove(ev.Name); err != nil && r.opts.Verbose {
					log.Printf("removing image %s: %v", ev.Name, err)
				}
				select {
				case r.imageEvents <- image.Event{Image: img}:
					last = now
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

// Close shuts down the recorder, stopping ffmpeg and removing the temporary directory.
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
