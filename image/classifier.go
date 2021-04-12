// Package image implements fetching images from video sources, and classifying
// images.
package image

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"log"
	"os"
	"time"

	edgeimpulse "github.com/edgeimpulse/linux-sdk-go"

	"github.com/disintegration/imaging"
)

// ClassifyEvent is the result of classifying one image.
type ClassifyEvent struct {
	// If not nil, an error occurred and other fields are not meaningful.
	Err error

	// The classification response from the model. Always a successful
	// response.
	edgeimpulse.RunnerClassifyResponse

	// How long classifying took.
	Classifying time.Duration

	// The image that was classified, after transforming to fit the
	// requirements for the model.
	Image image.Image
}

// Classifier receives images from a recorder, classifies them, and sends the
// results on channel Events.
type Classifier struct {
	Events chan ClassifyEvent

	recorder Recorder
	stop     chan struct{}
}

// ClassifierOpts are options for the classifier.
type ClassifierOpts struct {
	Verbose  bool   // Print verbose logging.
	TraceDir string // If not empty, directory to write images sent to runner.
}

// NewClassifier returns a new classifier that receives messages from recorder,
// classifies them using runner, and sends ClassifyEvents on its channel
// Events.
//
// Callers must call Close to clean up the classifier, and separately close the
// runner and recorder.
func NewClassifier(runner edgeimpulse.Runner, recorder Recorder, opts *ClassifierOpts) (*Classifier, error) {
	var xopts ClassifierOpts
	if opts != nil {
		xopts = *opts
	}

	modelParams := runner.ModelParameters()
	if modelParams.SensorType != edgeimpulse.SensorTypeCamera {
		return nil, fmt.Errorf("sensor for this model was %q, expected camera", modelParams.SensorType)
	}

	c := &Classifier{
		make(chan ClassifyEvent, 1),
		recorder,
		make(chan struct{}, 1),
	}

	imageEvents := recorder.Events()

	// Start at 2 to match the sequence numbers in the typical runner that uses message
	// ID's, with ID 1 for the hello transaction.
	seq := 2

	go func() {
		for {
			select {
			case <-c.stop:
				return
			case iev, ok := <-imageEvents:
				if !ok {
					return
				}
				if iev.Err != nil {
					c.Events <- ClassifyEvent{Err: iev.Err}
					continue
				}

				modelSize := image.Point{modelParams.ImageInputWidth, modelParams.ImageInputHeight}

				img := iev.Image
				imgSize := img.Bounds().Size()
				if imgSize != modelSize {
					if xopts.Verbose {
						log.Printf("resizing image from %v to %v", imgSize, modelSize)
					}
					img = imageResize(img, modelSize, xopts.Verbose)
				}

				if modelParams.ImageChannelCount == 3 {
					switch img.(type) {
					case *image.NRGBA:
					default:
						if xopts.Verbose {
							log.Printf("converting to nrgba image")
						}
						nimg := image.NewNRGBA(img.Bounds())
						draw.Draw(nimg, nimg.Bounds(), img, image.Point{}, draw.Src)
						img = nimg
					}
				} else {
					switch img.(type) {
					case *image.Gray:
					default:
						if xopts.Verbose {
							log.Printf("converting to gray image")
						}
						nimg := image.NewGray(img.Bounds())
						draw.Draw(nimg, nimg.Bounds(), img, image.Point{}, draw.Src)
						img = nimg
					}
				}

				data := make([]float64, modelSize.X*modelSize.Y)
				i := 0
				for y := 0; y < modelSize.Y; y++ {
					for x := 0; x < modelSize.X; x++ {
						r, g, b, _ := img.At(x, y).RGBA()
						r >>= 8
						g >>= 8
						b >>= 8
						v := (r << 16) | (g << 8) | b
						data[i] = float64(v)
						i++
					}
				}

				if xopts.TraceDir != "" {
					pngPath := fmt.Sprintf("%s/image-%d.png", xopts.TraceDir, seq)
					pf, err := os.Create(pngPath)
					if err != nil {
						log.Printf("trace, creating %s: %v", pngPath, err)
					} else {
						if err := png.Encode(pf, img); err != nil {
							log.Printf("trace, encoding png: %v", err)
						}
						if err := pf.Close(); err != nil {
							log.Printf("trace, closing file: %v", err)
						} else {
							log.Printf("trace %s", pngPath)
						}
					}
				}

				t0 := time.Now()
				resp, err := runner.Classify(data)
				if err != nil {
					c.Events <- ClassifyEvent{Err: err}
					continue
				}
				c.Events <- ClassifyEvent{nil, resp, time.Since(t0), iev.Image}
				seq++
			}
		}
	}()

	return c, nil
}

// Close shuts down the classifier.
// The runner and recorder must be stopped by the caller.
func (c *Classifier) Close() error {
	c.stop <- struct{}{}
	return nil
}

// imageResize resizes to the exact size. It crops part of the image to keep aspect ratio.
func imageResize(img image.Image, size image.Point, verbose bool) image.Image {
	t0 := time.Now()
	r := imaging.Fill(img, size.X, size.Y, imaging.Center, imaging.NearestNeighbor)
	if verbose {
		log.Printf("resizing in %v", time.Since(t0))
	}
	return r
}
