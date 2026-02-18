// Package audio implements reading audio samples and classifying samples.
package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"time"

	edgeimpulse "github.com/edgeimpulse/linux-sdk-go"
)

// ClassifyEvent is the result of classifying one audio slice.
type ClassifyEvent struct {
	// If set, an error occurred and other fields are not meaningful.
	Err error

	// The classification response from the model. Always a successful response.
	edgeimpulse.RunnerClassifyResponse

	// How long classifying took.
	Classifying time.Duration

	// The samples that were classified.
	Samples []float64
}

// ClassifierOpts are options for the classifier.
type ClassifierOpts struct {
	Verbose bool // Print verbose logging.
}

// Classifier continuously reads audio from a recorder, classifies them, and
// sends the results on channel Events.
type Classifier struct {
	Events chan ClassifyEvent
}

// NewClassifier starts an audio recorder, reads audio data, and classifies
// them every interval, sending the results on its channel Events.
//
// For models with UseContinuousMode=true (sliding window), the classifier
// sends SliceSize samples per call using ClassifyContinuous. For standard
// models it accumulates a full InputFeaturesCount window and calls Classify.
//
// Callers must call Close on the classifier to clean it up, and separately
// close the runner and recorder.
func NewClassifier(runner edgeimpulse.Runner, recorder Recorder, interval time.Duration, opts *ClassifierOpts) (*Classifier, error) {
	var xopts ClassifierOpts
	if opts != nil {
		xopts = *opts
	}

	modelParams := runner.ModelParameters()
	if modelParams.SensorType != edgeimpulse.SensorTypeMicrophone {
		return nil, fmt.Errorf("sensor for this model was %q, expected microphone", modelParams.SensorType)
	}

	c := &Classifier{
		make(chan ClassifyEvent, 1),
	}

	// Determine whether to use continuous (sliding-window) mode.
	continuous := modelParams.UseContinuousMode && modelParams.SliceSize > 0

	// intervalSampleCount: how many samples we read per interval tick.
	intervalSampleCount := int(modelParams.Frequency * interval.Seconds())
	intervalBuf := make([]byte, 2*intervalSampleCount) // single channel, 16-bit PCM

	// modelSamples holds the full window (InputFeaturesCount) for standard mode,
	// or the slice window (SliceSize) for continuous mode.
	windowSize := modelParams.InputFeaturesCount
	if continuous {
		windowSize = modelParams.SliceSize
	}
	modelSamples := make([]float64, windowSize)
	modelSampleCount := 0

	audio := recorder.Reader()
	samples := make(chan []float64)

	// Classifier goroutine: receives sample slices and calls the model.
	go func() {
		for {
			s, ok := <-samples
			if !ok {
				return
			}
			t0 := time.Now()
			var (
				resp edgeimpulse.RunnerClassifyResponse
				err  error
			)
			if continuous {
				resp, err = runner.ClassifyContinuousWithContext(context.Background(), s)
			} else {
				resp, err = runner.ClassifyWithContext(context.Background(), s)
			}
			if err != nil {
				c.Events <- ClassifyEvent{Err: err}
				return
			}
			c.Events <- ClassifyEvent{nil, resp, time.Since(t0), s}
		}
	}()

	// Reader goroutine: reads audio from the recorder and fills the window.
	go func() {
		defer close(samples)

		for {
			// Read one interval-sized buffer of audio.
			if _, err := io.ReadFull(audio, intervalBuf); err != nil {
				c.Events <- ClassifyEvent{Err: fmt.Errorf("reading audio: %w", err)}
				return
			}

			// The interval may be longer than the window. If so, only use the end.
			buf := intervalBuf
			sampleCount := intervalSampleCount
			if sampleCount > windowSize {
				sampleCount = windowSize
				buf = buf[2*(intervalSampleCount-sampleCount):]
			}

			// Shift the window left to make room for new samples at the end.
			start := modelSampleCount
			end := start + sampleCount
			if end > windowSize {
				n := end - windowSize
				copy(modelSamples, modelSamples[n:])
				start -= n
				modelSampleCount -= n
			}

			r := bytes.NewReader(buf)
			for i := 0; i < sampleCount; i++ {
				var v int16
				binary.Read(r, binary.LittleEndian, &v) //nolint:errcheck
				modelSamples[start+i] = float64(v)
			}
			modelSampleCount += sampleCount

			if modelSampleCount < windowSize {
				// Haven't filled the window yet.
				continue
			}

			if xopts.Verbose {
				log.Printf("classifying %d samples (continuous=%v)", windowSize, continuous)
			}

			// Copy samples so the classifier goroutine gets a stable slice.
			s := make([]float64, windowSize)
			copy(s, modelSamples)
			select {
			case samples <- s:
			default:
				if xopts.Verbose {
					log.Printf("dropping samples, classifier still busy")
				}
			}

			// In continuous mode, the window slides: we keep the existing samples
			// and let new interval data overwrite the oldest portion next iteration.
			// In standard mode, we reset the window after each classify call so we
			// always classify a fresh full window.
			if !continuous {
				modelSampleCount = 0
			}
		}
	}()

	return c, nil
}

// Close shuts down the classifier.
// Close does not close the runner or recorder.
func (c *Classifier) Close() error {
	return nil
}
