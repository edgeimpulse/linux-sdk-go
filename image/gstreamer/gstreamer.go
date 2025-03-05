// Package gstreamer implements an image recorder with the gstreamer tools.
package gstreamer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	edgeimpulse "github.com/edgeimpulse/linux-sdk-go"
	"github.com/edgeimpulse/linux-sdk-go/image"

	"github.com/fsnotify/fsnotify"
)

var errInstallHint = errors.New("executable not found, install with: sudo apt install -y gstreamer1.0-tools gstreamer1.0-plugins-good gstreamer1.0-plugins-base gstreamer1.0-plugins-base-apps")

// RecorderOpts has options for a new gstreamer recorder.
type RecorderOpts struct {
	Verbose  bool
	Interval time.Duration // How often to record an image.
	DeviceID string        // As retrieved from ListDevices. If empty, NewRecorder will use the first device returned by ListDevices.
}

// Recorder is an image recorder using gstreamer.
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

type device struct {
	ID          string
	Name        string
	DeviceClass string
	RawCaps     []string
	Caps        []image.DeviceCap
	inCapMode   bool
}

var widthRegexp = regexp.MustCompile("width=([0-9]+)[^0-9]")
var heightRegexp = regexp.MustCompile("height=([0-9]+)[^0-9]")
var framerateRegexp = regexp.MustCompile("framerate=([0-9]+)[^0-9]")
var argusRegexp = regexp.MustCompile("GST_ARGUS:[ \n\r\t]*([0-9]+)[ \n\r\t]*x[ \n\r\t]*([0-9]+)[^=]*=[ \n\r\t]*([0-9,]+)[ \n\r\t]*fps")

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// ListDevices returns a list of devices that can be used for recording.
// ListDevices returns an error if no devices are available.
func ListDevices() ([]image.Device, error) {
	cmd := exec.Command("gst-device-monitor-1.0")
	buf, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			err = errInstallHint
		}
		return nil, fmt.Errorf("listing devices using gst-device-monitor-1.0: %v", err)
	}

	var r []device
	var d *device
	b := bufio.NewScanner(bytes.NewReader(buf))
	for b.Scan() {
		s := strings.TrimSpace(b.Text())
		if s == "" {
			continue
		}
		if s == "Device found:" {
			if d != nil {
				r = append(r, *d)
			}
			d = &device{RawCaps: []string{}, Caps: []image.DeviceCap{}}
			continue
		}

		if d == nil {
			continue
		}

		if strings.HasPrefix(s, "name  :") {
			d.Name = strings.TrimSpace(strings.SplitN(s, ":", 2)[1])
			continue
		}
		if strings.HasPrefix(s, "class :") {
			d.DeviceClass = strings.TrimSpace(strings.SplitN(s, ":", 2)[1])
			continue
		}
		if strings.HasPrefix(s, "caps  :") {
			cap := strings.TrimSpace(strings.SplitN(s, ":", 2)[1])
			d.RawCaps = append(d.RawCaps, cap)
			d.inCapMode = true
			continue
		}
		if strings.HasPrefix(s, "properties:") {
			d.inCapMode = false
			continue
		}
		if d.inCapMode {
			d.RawCaps = append(d.RawCaps, s)
		}
		if strings.HasPrefix(s, "device.path =") {
			d.ID = strings.TrimSpace(strings.SplitN(s, "=", 2)[1])
		}
	}
	if err := b.Err(); err != nil {
		return nil, err
	}

	if d != nil && d.ID != "" {
		r = append(r, *d)
	}

	var devs []image.Device
	for _, d := range r {
		if d.DeviceClass != "Video/Source" {
			continue
		}
		for _, rc := range d.RawCaps {
			var t string
			switch {
			case strings.HasPrefix(rc, "video/x-raw"):
				t = "video/x-raw"
			case strings.HasPrefix(rc, "image/jpeg"):
				t = "image/jpeg"
			default:
				continue
			}
			mw := widthRegexp.FindStringSubmatch(rc)
			mh := heightRegexp.FindStringSubmatch(rc)
			mf := framerateRegexp.FindStringSubmatch(rc)
			if mw == nil || mh == nil || mf == nil {
				continue
			}
			width, werr := strconv.ParseInt(mw[1], 10, 32)
			height, herr := strconv.ParseInt(mh[1], 10, 32)
			framerate, ferr := strconv.ParseInt(mf[1], 10, 32)
			if werr != nil || herr != nil || ferr != nil {
				continue
			}
			if width != 0 && height != 0 && framerate != 0 {
				d.Caps = append(d.Caps, image.DeviceCap{
					Type:      t,
					Width:     int(width),
					Height:    int(height),
					Framerate: int(framerate),
				})
			}
		}
		if len(d.Caps) == 0 {
			continue
		}

		// If we have a video cap, only return those.
		nc := []image.DeviceCap{}
		for _, c := range d.Caps {
			if c.Type == "video/x-raw" {
				nc = append(nc, c)
			}
		}
		if len(nc) > 0 {
			d.Caps = nc
		}

		distance := func(a image.DeviceCap) int {
			return abs(a.Width-640)*abs(a.Height-480) + abs(a.Width-640) + abs(a.Height-480)
		}

		sort.Slice(d.Caps, func(i, j int) bool {
			return distance(d.Caps[i]) < distance(d.Caps[j])
		})

		devs = append(devs, image.Device{
			ID:   d.ID,
			Name: d.Name,
			Caps: d.Caps,
		})
	}

	nvdevs, err := listNvarguscamerasrcDevices()
	if err != nil && len(devs) == 0 {
		return nil, err
	}
	devs = append(devs, nvdevs...)

	if len(devs) == 0 {
		return nil, fmt.Errorf("no devices found")
	}

	return devs, nil
}

func listNvarguscamerasrcDevices() ([]image.Device, error) {
	cmd := exec.Command("gst-inspect-1.0")
	buf, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			err = errInstallHint
		}
		return nil, fmt.Errorf("running gst-inspect-1.0 to check for nvarguscamerasrc: %v", err)
	}
	if !strings.Contains(string(buf), "nvarguscamerasrc") {
		return nil, nil
	}

	cmd = exec.Command("gst-launch-1.0", "nvarguscamerasrc")
	buf, err = cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("running gst-launch-1.0 nvarguscamerasrc: %v", err)
	}

	var caps []image.DeviceCap

	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		m := argusRegexp.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		width, werr := strconv.ParseInt(m[1], 10, 32)
		height, herr := strconv.ParseInt(m[2], 10, 32)
		framerate, ferr := strconv.ParseInt(strings.Split(m[3], ".")[0], 10, 32)
		if werr != nil || herr != nil || ferr != nil {
			continue
		}

		if width == 0 || height == 0 || framerate == 0 {
			continue
		}

		cap := image.DeviceCap{
			Type:      "nvarguscamerasrc",
			Width:     int(width),
			Height:    int(height),
			Framerate: int(framerate),
		}
		caps = append(caps, cap)
	}

	if len(caps) == 0 {
		return nil, nil
	}

	dev := image.Device{
		ID:   "nvarguscamerasrc",
		Name: "CSI camera",
		Caps: caps,
	}
	return []image.Device{dev}, nil
}

// NewRecorder creates a new recorder using gstream. Gstreamer writes images to a
// temporary directory. These files are read and sent over the channel returned
// by Events.
//
// Callers must call Close to clean up.
func NewRecorder(opts RecorderOpts) (recorder *Recorder, rerr error) {
	r := &Recorder{}
	r.opts = opts

	devices, err := ListDevices()
	if err != nil {
		return nil, fmt.Errorf("listing devices: %v", err)
	}
	var dev image.Device
	if r.opts.DeviceID == "" {
		dev = devices[0]
		r.opts.DeviceID = dev.ID
	} else {
		for _, d := range devices {
			if d.ID == r.opts.DeviceID {
				dev = d
				break
			}
		}
		if dev.ID == "" {
			return nil, fmt.Errorf("device not found")
		}
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
		log.Printf("gstreamer recorder, writing images to tempdir %s", r.tempDir)
	}

	// We want the first device config of at least 640x480, and otherwise just default to that resolution.
	var cap *image.DeviceCap
	for _, c := range dev.Caps {
		if c.Width >= 640 && c.Height >= 480 {
			cap = &c
			break
		}
	}
	if cap == nil {
		cap = &image.DeviceCap{
			Type:      "video/x-raw",
			Width:     640,
			Height:    480,
			Framerate: 30,
		}
	}

	var args []string
	switch cap.Type {
	case "video/x-raw":
		args = []string{
			"v4l2src",
			"device=" + r.opts.DeviceID,
			// "num-buffers=999999999",
			"!",
			fmt.Sprintf("video/x-raw,width=%d,height=%d", cap.Width, cap.Height),
			"!",
			"videoconvert",
			"!",
			"jpegenc",
			"!",
			"multifilesink",
			"location=" + r.tempDir + "/test%05d.jpg",
		}
	case "image/jpeg":
		args = []string{
			"v4l2src",
			"device=" + r.opts.DeviceID,
			// "num-buffers=999999999",
			"!",
			fmt.Sprintf("image/jpeg,width=%d,height=%d", cap.Width, cap.Height),
			"!",
			"multifilesink",
			"location=" + r.tempDir + "/test%05d.jpg",
		}
	case "nvarguscamerasrc":
		args = []string{
			"nvarguscamerasrc",
			"!",
			fmt.Sprintf("video/x-raw(memory:NVMM),width=%d,height=%d", cap.Width, cap.Height),
			"!",
			"nvvidconv",
			"flip-method=0",
			"!",
			fmt.Sprintf("video/x-raw,width=%d,height=%d", cap.Width, cap.Height),
			"!",
			"nvvidconv",
			"!",
			"jpegenc",
			"!",
			"multifilesink",
			"location=test%05d.jpg",
		}
	default:
		return nil, fmt.Errorf("unknown device capability type %q", cap.Type)
	}

	if r.opts.Verbose {
		log.Printf("starting gstreamer as gst-launch-1.0 %s", strings.Join(args, " "))
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	cmd := exec.CommandContext(ctx, "gst-launch-1.0", args...)
	cmd.Dir = r.tempDir
	if r.opts.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			err = errInstallHint
		}
		return nil, fmt.Errorf("starting gstreamer with gst-launch-1.0: %v", err)
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
		var last time.Time
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if ev.Op == fsnotify.Remove || !strings.HasSuffix(ev.Name, ".jpg") {
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

// Close shuts down the recorder, stopping gstreamer and removing the temporary
// directory.
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
