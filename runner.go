// Package edgeimpulse lets you run model processes to classify measurements.
package edgeimpulse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Runner is a running model with model and project parameters, and the ability
// to classify data.
type Runner interface {
	ModelParameters() ModelParameters
	Project() Project
	Classify(data []float64) (RunnerClassifyResponse, error)
	Close() error
}

// RunnerProcess is a running model process that can classify data.
type RunnerProcess struct {
	modelParams ModelParameters
	project     Project
	opts        RunnerOpts
	tempDir     string             // Temp dir created for this runner if any. Removed on close.
	cancel      context.CancelFunc // For stopping model process.
	conn        net.Conn           // Unix domain socket to model process.
	mutex       sync.Mutex         // Serializing writing requests to model process.
	lastID      int64
}

// ModelParameters returns the parameters for this runner.
func (r *RunnerProcess) ModelParameters() ModelParameters {
	return r.modelParams
}

// Project returns the project for this runner.
func (r *RunnerProcess) Project() Project {
	return r.project
}

// Ensure that RunnerProcess implements interface Runner.
var _ Runner = (*RunnerProcess)(nil)

// RunnerResponse represents the basic status of a response from the model.
type RunnerResponse struct {
	ID      int64  `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`

	runnerResponser
}

type runnerResponser interface {
	runnerResponse() RunnerResponse
}

func (r RunnerResponse) runnerResponse() RunnerResponse {
	return r
}

// runnerHelloRequest is a request to the model for its parameters.
type runnerHelloRequest struct {
	ID    int64 `json:"id"`
	Hello int   `json:"hello"` // 1
}

// ModelType can be "classification" or "object_detection". May be expanded in
// the future.
type ModelType string

const (
	// ModelTypeClassification indicates the model returns scoring values
	// for a set of labels.
	ModelTypeClassification ModelType = "classification"

	// ModelTypeObjectDetection indicates the model returns returns
	// bounding boxes for recognized objects.
	ModelTypeObjectDetection ModelType = "object_detection"
)

// SensorType describes the source of measurements/values.
type SensorType string

// SensorTypes as source of measurements. Use a matching recorder and
// classifier, e.g. an audio classifier with a SensorTypeMicrophone.
const (
	SensorTypeUnknown       SensorType = "unknown"
	SensorTypeMicrophone    SensorType = "microphone"
	SensorTypeAccelerometer SensorType = "accelerometer"
	SensorTypeCamera        SensorType = "camera"
)

// ModelParameters holds the model parameters for a model.
type ModelParameters struct {
	ModelType  ModelType `json:"model_type"`
	Sensor     int64     `json:"sensor"`
	SensorType SensorType
	IntervalMS float64 `json:"interval_ms"`

	Frequency float64 `json:"frequency"`

	InputFeaturesCount int `json:"input_features_count"`

	// For images only.
	ImageInputHeight  int `json:"image_input_height"`
	ImageInputWidth   int `json:"image_input_width"`
	ImageChannelCount int `json:"image_channel_count"`

	// Labels in resulting classifications.
	Labels     []string `json:"labels"`
	LabelCount int      `json:"label_count"`

	HasAnomaly float64 `json:"has_anomaly"`
}

// String returns a human-readable summary of the model parameters.
func (p ModelParameters) String() string {
	var s string
	switch p.SensorType {
	case SensorTypeMicrophone:
		s = fmt.Sprintf("microphone, frequency %vHz, window length %v", p.Frequency, time.Duration(float64(p.InputFeaturesCount)/p.Frequency)*time.Second)
	case SensorTypeAccelerometer:
		s = fmt.Sprintf("accelerometer, frequency %vHz, window length %v", p.Frequency, time.Duration(float64(p.InputFeaturesCount)/p.Frequency/3)*time.Second)
	case SensorTypeCamera:
		s = fmt.Sprintf("camera, %dx%d (%d channels)", p.ImageInputWidth, p.ImageInputHeight, p.ImageChannelCount)
	default:
		s = fmt.Sprintf("model type %s, sensor type %s (%d)", p.ModelType, p.SensorType, p.Sensor)
	}
	if len(p.Labels) > 0 {
		s += ", classes " + strings.Join(p.Labels, ",")
	}
	return s
}

// Project holds the project information stored in the model, originally from
// EdgeImpulse Studio.
type Project struct {
	DeployVersion int64  `json:"deploy_version"`
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Owner         string `json:"owner"`
}

// String returns human-readable project info.
func (p Project) String() string {
	return fmt.Sprintf("%s/%s (v%v)", p.Owner, p.Name, p.DeployVersion)
}

// runnerHelloResponse is the response from the model to a runnerHelloRequest.
type runnerHelloResponse struct {
	RunnerResponse
	ModelParameters ModelParameters `json:"model_parameters"`
	Project         Project         `json:"project"`
}

// RunnerClassifyRequest is a request to the model to classify data.
type RunnerClassifyRequest struct {
	ID       int64     `json:"id"`
	Classify []float64 `json:"classify"`
}

// RunnerClassifyResponse is the response from the model to a
// RunnerClassifyRequest.
type RunnerClassifyResponse struct {
	RunnerResponse

	Result struct {
		// Based on the ModelType, either Classification or BoundingBoxes will be set.
		Classification map[string]float64 `json:"classification,omitempty"`

		BoundingBoxes []struct {
			Label  string  `json:"label"`
			Value  float64 `json:"value"`
			X      int     `json:"x"`
			Y      int     `json:"y"`
			Width  int     `json:"width"`
			Height int     `json:"height"`
		} `json:"bounding_boxes,omitempty"`

		Anomaly float64 `json:"anomaly,omitempty"`
	} `json:"result"`

	Timing struct {
		DSP            float64 `json:"dsp"`
		Classification float64 `json:"classification"`
		Anomaly        float64 `json:"anomaly"`
	} `json:"timing"`
}

// String returns a summary of the result, with classification or error
// message.
func (r RunnerClassifyResponse) String() string {
	if !r.Success {
		return fmt.Sprintf("error: %v", r.Error)
	}
	ms := fmt.Sprintf("%dms", int64(r.Timing.Classification))
	var anomaly string
	if r.Result.Anomaly != 0 {
		anomaly = fmt.Sprintf(" anomaly=%.4f", r.Result.Anomaly)
	}
	if r.Result.Classification != nil {
		var kv []string
		for k, v := range r.Result.Classification {
			kv = append(kv, fmt.Sprintf("%s=%.4f", k, v))
		}
		sort.Slice(kv, func(i, j int) bool {
			return kv[i] < kv[j]
		})
		return fmt.Sprintf("classification in %s: %s%s", ms, strings.Join(kv, " "), anomaly)
	} else if r.Result.BoundingBoxes != nil {
		var boxes []string
		for _, b := range r.Result.BoundingBoxes {
			boxes = append(boxes, fmt.Sprintf("x=%d,y=%d,width=%d,height=%d,label=%s,value=%.4f", b.X, b.Y, b.Width, b.Height, b.Label, b.Value))
		}
		return fmt.Sprintf("boundingboxes in %s: %s%s", ms, strings.Join(boxes, ", "), anomaly)
	}
	return "(result without classification and bounding boxes)"
}

// RunnerOpts contains options for starting a runner.
type RunnerOpts struct {
	// Explicitly set a working directory. This directory is not
	// automatically removed on Runner.Close. If empty, a temporary
	// directory is created.
	WorkDir string

	// If not empty, the JSON-encoded requests and responses are written to
	// this directory.
	TraceDir string
}

// NewRunnerProcess creates and starts a new runner from a model file.
// Always call Close on a runner, to cleanup any temporary directories.
func NewRunnerProcess(modelPath string, opts *RunnerOpts) (runner *RunnerProcess, rerr error) {
	var err error
	modelPath, err = filepath.Abs(modelPath)
	if err != nil {
		return nil, fmt.Errorf("absolute path for modelPath %q: %v", modelPath, err)
	}

	r := &RunnerProcess{}
	if opts != nil {
		r.opts = *opts
	}

	// Make sure we cleanup on failure.
	defer func() {
		if rerr != nil {
			r.Close()
		}
	}()

	if r.opts.WorkDir == "" {
		dir, err := TempDir()
		if err != nil {
			return nil, fmt.Errorf("making temp dir: %v", err)
		}
		r.opts.WorkDir = dir
		r.tempDir = dir
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	cmd := exec.CommandContext(ctx, modelPath, "runner.sock")
	cmd.Dir = r.opts.WorkDir
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting model process: %v", err)
	}
	go cmd.Wait()

	sockPath := r.opts.WorkDir + "/runner.sock"
	for i := 0; ; i++ {
		conn, err := net.Dial("unix", sockPath)
		if err == nil {
			r.conn = conn
			break
		}
		if !errors.Is(err, syscall.ENOENT) {
			return nil, fmt.Errorf("opening runner socket: %v", err)
		}
		if i == 1000 {
			return nil, fmt.Errorf("no socket from runner")
		}
		time.Sleep(1 * time.Millisecond)
	}

	helloReq := runnerHelloRequest{ID: r.nextID(), Hello: 1}
	var helloResp runnerHelloResponse
	if err := r.transact(helloReq.ID, helloReq, &helloResp); err != nil {
		return nil, fmt.Errorf("hello to model: %v", err)
	}
	mp := helloResp.ModelParameters
	if string(mp.ModelType) == "" {
		mp.ModelType = ModelTypeClassification
	}
	switch mp.Sensor {
	default:
		mp.SensorType = SensorTypeUnknown
	case 1:
		mp.SensorType = SensorTypeMicrophone
	case 2:
		mp.SensorType = SensorTypeAccelerometer
	case 3:
		mp.SensorType = SensorTypeCamera
	}
	r.modelParams = mp
	r.project = helloResp.Project

	return r, nil
}

// Do a single request/response transaction.
func (r *RunnerProcess) transact(id int64, req interface{}, resp runnerResponser) error {
	if err := json.NewEncoder(r.conn).Encode(req); err != nil {
		return fmt.Errorf("writing json to model: %v", err)
	}

	r.writeTrace(fmt.Sprintf("%s/runner-%d-request.json", r.opts.TraceDir, id), req)

	r.conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	dec := json.NewDecoder(r.conn)
	if err := dec.Decode(resp); err != nil {
		return fmt.Errorf("reading json from model: %v", err)
	}

	r.writeTrace(fmt.Sprintf("%s/runner-%d-response.json", r.opts.TraceDir, id), resp)

	// Model writes a zero byte after the JSON. It's probably already read, and buffered in the decoder, but not necessarily. So make sure to drain it.
	buf, err := ioutil.ReadAll(dec.Buffered())
	if err == nil && len(buf) == 0 {
		r.conn.Read([]byte{0})
	}

	if !resp.runnerResponse().Success {
		return fmt.Errorf("classifying: %s", resp.runnerResponse().Error)
	}
	return nil
}

func (r *RunnerProcess) writeTrace(filename string, data interface{}) {
	if r.opts.TraceDir == "" {
		return
	}

	f, err := os.Create(filename)
	if err != nil {
		log.Printf("trace, creating %s: %v", filename, err)
		return
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(data); err != nil {
		log.Printf("trace, writing data: %v", err)
	}
	log.Printf("trace %s", filename)
}

func (r *RunnerProcess) nextID() int64 {
	r.lastID++
	return r.lastID
}

// Classify executes the model on the features and returns the resulting
// classification.
func (r *RunnerProcess) Classify(data []float64) (resp RunnerClassifyResponse, rerr error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	req := RunnerClassifyRequest{
		ID:       r.nextID(),
		Classify: data,
	}
	rerr = r.transact(req.ID, req, &resp)
	return
}

// Close shuts down the runner, stopping the model process.
func (r *RunnerProcess) Close() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.cancel != nil {
		r.cancel()
	}
	if r.conn != nil {
		r.conn.Close()
	}
	if r.tempDir != "" {
		os.RemoveAll(r.tempDir)
	}
	return nil
}
