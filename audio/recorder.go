package audio

import (
	"io"
)

// Recorder is a source of audio samples.
type Recorder interface {
	// Reader returns a source from which audio samples can be read.
	Reader() io.Reader

	// Close shuts down the recorder prevent further successful reads from
	// the audio source.
	Close() error
}
