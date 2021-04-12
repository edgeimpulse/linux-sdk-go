// Package audio implements reading audio samples and classifying samples.
package audio

import (
	"bytes"
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

	// The image that was classified, after transforming to fit the
	// requirements for the model.
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

	// We keep reading an interval worth of audio data. We keep track of a
	// full frame with the size the model needs. So the new interval-slice
	// of samples is appended, and oldest data chopped off.
	intervalSampleCount := int(modelParams.Frequency * interval.Seconds())
	intervalBuf := make([]byte, 2*intervalSampleCount) // For single channel, 16 bit samples.
	modelSamples := make([]float64, modelParams.InputFeaturesCount)
	modelSampleCount := 0

	audio := recorder.Reader()
	samples := make(chan []float64)

	go func() {
		for {
			s, ok := <-samples
			if !ok {
				return
			}
			t0 := time.Now()
			resp, err := runner.Classify(s)
			if err != nil {
				c.Events <- ClassifyEvent{Err: err}
				return
			}
			c.Events <- ClassifyEvent{nil, resp, time.Since(t0), s}
		}
	}()

	go func() {
		// When we stop, also stop the classifier.
		defer func() {
			close(samples)
		}()

		for {
			// Read one interval-sized buffer of audio.
			if _, err := io.ReadFull(audio, intervalBuf); err != nil {
				c.Events <- ClassifyEvent{Err: fmt.Errorf("reading audio: %v", err)}
				return
			}

			// The interval may be longer than the model needs. If so, only use the end of the buffer.
			buf := intervalBuf
			sampleCount := intervalSampleCount
			if sampleCount > len(modelSamples) {
				sampleCount = len(modelSamples)
				buf = buf[2*(intervalSampleCount-sampleCount):]
			}

			// Make room for the new samples at the end of the samples buffer, overwriting leading/old samples.
			start := modelSampleCount
			end := start + sampleCount
			if end > len(modelSamples) {
				n := end - len(modelSamples)
				copy(modelSamples, modelSamples[n:])
				start -= n
				modelSampleCount -= n
			}

			r := bytes.NewReader(buf)
			for i := 0; i < sampleCount; i++ {
				var v int16
				binary.Read(r, binary.LittleEndian, &v)
				modelSamples[start+i] = float64(v)
			}
			modelSampleCount += sampleCount

			if modelSampleCount < len(modelSamples) {
				continue
			}

			// Copy samples so we don't interfere with existing classifier.
			// This creates a lot of garbage for the collector, might want to change in the future.
			s := make([]float64, len(modelSamples))
			copy(s, modelSamples)
			select {
			case samples <- s:
			default:
				if xopts.Verbose {
					log.Printf("dropping samples, classifier still busy")
				}
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
