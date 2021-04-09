package image

import (
	"image"
)

// Recorder is a source of images, for example a webcam.
type Recorder interface {
	// Events returns a channel from which ImageEvents can be read, each containing an image.
	Events() chan Event

	// Close shuts down the image recorder. No further ImageEvents will be sent.
	Close() error
}

// Event is a single image (or error) coming from a Recorder.
type Event struct {
	// If set, an error occurred.
	Err error

	// Image read from recorder. If Err is set, Image is not valid.
	Image image.Image
}
