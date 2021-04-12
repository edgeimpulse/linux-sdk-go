// Package audiocmd implements reading audio samples by executing an external
// command.
package audiocmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/edgeimpulse/linux-sdk-go/audio"
)

var errSoxInstallHint = errors.New("sox executable not found, install with: sudo apt install -y sox")

// RecorderOpts holds option for a Recorder.
type RecorderOpts struct {
	SampleRate     int
	Channels       int
	Compress       bool
	Threshold      float64
	ThresholdStart *float64
	ThresholdEnd   *float64
	Silence        float64
	Verbose        bool
	RecordProgram  string // "sox", "rec", "arecord"
	AudioType      string
	AsRaw          bool
	DeviceID       string
}

// recorderOptsDefault has default option values for a Recorder.
var recorderOptsDefault = RecorderOpts{
	SampleRate:    16000,
	Channels:      1,
	Threshold:     0.5,
	Silence:       1.0,
	RecordProgram: "sox",
	AudioType:     "wav",
}

// Recorder is a source of audio samples.
type Recorder struct {
	audio  io.ReadCloser
	opts   RecorderOpts
	cancel context.CancelFunc
}

// Ensure that Recorder implements the Recorder interface.
var _ audio.Recorder = (*Recorder)(nil)

// ListDevices returns audio recording devices available on the system.
func ListDevices() ([]audio.Device, error) {
	var r []audio.Device

	f, err := os.Open("/proc/asound/cards")
	if err == nil {
		defer f.Close()
		r, err = parseAsoundCards(f)
		if err != nil {
			return nil, err
		}
	} else if runtime.GOOS == "darwin" {
		cmd := exec.Command("sox", "-V6", "-n", "-t", "coreaudio", "doesnotexist")
		// The command is meant to fail, we just wants its output that lists the audio devices.
		output, err := cmd.CombinedOutput()
		if err != nil && errors.Is(err, exec.ErrNotFound) {
			return nil, errSoxInstallHint
		}
		r, err = parseSoxDevices(string(output))
		if err != nil {
			return nil, err
		}
	}
	if len(r) == 0 {
		r = []audio.Device{
			{
				ID:   "",
				Name: "Default microphone",
			},
		}
	}
	return r, nil
}

var asoundRegexp = regexp.MustCompile(`^[ \t]*([0-9]*) [^\]]*\]: (.*)$`)

func parseAsoundCards(f io.Reader) ([]audio.Device, error) {
	var r []audio.Device

	b := bufio.NewScanner(f)
	for b.Scan() {
		m := asoundRegexp.FindStringSubmatch(b.Text())
		if m != nil {
			r = append(r, audio.Device{
				ID:   fmt.Sprintf("hw:%s,0", m[1]),
				Name: m[2],
			})
		}
	}
	if err := b.Err(); err != nil {
		return nil, fmt.Errorf("parsing list of sound cards: %v", err)
	}
	return r, nil
}

var soxRegexp = regexp.MustCompile(`^sox INFO coreaudio: Found Audio Device "(.*)"$`)

func parseSoxDevices(s string) ([]audio.Device, error) {
	var r []audio.Device
	seen := map[string]struct{}{}

	lines := strings.Split(s, "\n")
	for _, line := range lines {
		m := soxRegexp.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := m[1]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		r = append(r, audio.Device{
			ID:   id,
			Name: id,
		})
	}
	return r, nil
}

// NewRecorder starts a new command that records audio samples.
// Recorder implements the audio.Recorder interface.
//
// Opts and its fields can be nil or zero, in which case default values are
// used.
func NewRecorder(opts *RecorderOpts) (recorder *Recorder, rerr error) {
	var xopts RecorderOpts
	if opts != nil {
		xopts = *opts
	}
	if xopts.SampleRate == 0 {
		xopts.SampleRate = recorderOptsDefault.SampleRate
	}
	if xopts.Channels == 0 {
		xopts.Channels = recorderOptsDefault.Channels
	}
	if xopts.Threshold == 0 {
		xopts.Threshold = recorderOptsDefault.Threshold
	}
	if xopts.RecordProgram == "" {
		xopts.RecordProgram = recorderOptsDefault.RecordProgram
	}
	if xopts.AudioType == "" {
		xopts.AudioType = recorderOptsDefault.AudioType
	}

	audioType := xopts.AudioType

	var args []string
	switch xopts.RecordProgram {
	case "sox":
		if xopts.AsRaw {
			audioType = "raw"
		}
		args = []string{"-d"}
		if xopts.DeviceID != "" {
			switch runtime.GOOS {
			case "linux":
				args = []string{"-t", "alsa", xopts.DeviceID}
			case "darwin":
				args = []string{"-t", "coreaudio", xopts.DeviceID}
			default:
				return nil, fmt.Errorf("cannot set deviceID on this OS")
			}
		}
		args = append(args,
			"-q", // show no progress
			"-r", fmt.Sprintf("%d", xopts.SampleRate),
			"-c", "1", // channels
			"-e", "signed-integer", // sample encoding
			"-b", "16", // precision (bits)
			"-t", audioType,
			"-",
		)
	case "rec":
		thresholdStart := fmt.Sprintf("%v%%", xopts.Threshold)
		if xopts.ThresholdStart != nil {
			thresholdStart = fmt.Sprintf("%v", *xopts.ThresholdStart)
		}
		thresholdEnd := fmt.Sprintf("%v%%", xopts.Threshold)
		if xopts.ThresholdEnd != nil {
			thresholdEnd = fmt.Sprintf("%v", *xopts.ThresholdEnd)
		}
		args = []string{
			"-q", // show no progress
			"-r", fmt.Sprintf("%d", xopts.SampleRate),
			"-c", fmt.Sprintf("%d", xopts.Channels),
			"-e", "signed-integer",
			"-b", "16", // precision (bits)
			"-t", audioType,
			"-", // pipe
			// end on silence
			"silence", "1", "0.1", thresholdStart,
			"1", fmt.Sprintf("%v", xopts.Silence),
			thresholdEnd,
		}
	case "arecord":
		args = []string{
			"-q", // show no progress
			"-r", fmt.Sprintf("%d", xopts.SampleRate),
			"-c", fmt.Sprintf("%d", xopts.Channels),
			"-t", audioType,
			"-f", "S16_LE",
			"-", // pipe
		}
		if xopts.DeviceID != "" {
			args = append([]string{"-D", xopts.DeviceID}, args...)
		}
	default:
		return nil, fmt.Errorf("unknown RecordProgram %q", xopts.RecordProgram)
	}

	r := &Recorder{opts: xopts}

	// Ensure cleanup on failure.
	defer func() {
		if rerr != nil {
			r.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	cmd := exec.CommandContext(ctx, xopts.RecordProgram, args...)
	audio, err := cmd.StdoutPipe()
	if err != nil {
		if xopts.RecordProgram == "sox" && errors.Is(err, exec.ErrNotFound) {
			return nil, errSoxInstallHint
		}
		return nil, fmt.Errorf("stdout pipe: %v", err)
	}
	r.audio = audio

	if xopts.Verbose {
		log.Printf("Recording %d channels with sample rate %d...", xopts.Channels, xopts.SampleRate)
		log.Printf("Command %s", strings.Join(append([]string{xopts.RecordProgram}, args...), " "))
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting recorder: %v", err)
	}

	return r, nil
}

// Reader returns a source from which audio samples can be read.
func (r *Recorder) Reader() io.Reader {
	return r.audio
}

// Close stops the command recording audio, and prevents further successful reads on the audio source.
func (r *Recorder) Close() error {
	r.cancel()
	r.audio.Close()
	return nil
}
