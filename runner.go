// Package edgeimpulse lets you run model processes to classify measurements.
package edgeimpulse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Runner is a running model with model and project parameters, and the ability
// to classify data.
type Runner interface {
	ModelParameters() ModelParameters
	Project() Project
	Classify(data []float64) (RunnerClassifyResponse, error)
	ClassifyWithContext(ctx context.Context, data []float64) (RunnerClassifyResponse, error)
	ClassifyContinuous(data []float64) (RunnerClassifyResponse, error)
	ClassifyContinuousWithContext(ctx context.Context, data []float64) (RunnerClassifyResponse, error)
	SetThreshold(ctx context.Context, threshold BlockThreshold) error
	SetParameter(ctx context.Context, params map[string]any) error
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
	lastID      atomic.Int64
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

// ModelType can be "classification", "object_detection", "regression", etc.
type ModelType string

const (
	// ModelTypeClassification indicates the model returns scoring values
	// for a set of labels.
	ModelTypeClassification ModelType = "classification"

	// ModelTypeObjectDetection indicates the model returns bounding boxes
	// for recognized objects.
	ModelTypeObjectDetection ModelType = "object_detection"

	// ModelTypeConstrainedObjectDetection is a variant of object detection used
	// by FOMO models.
	ModelTypeConstrainedObjectDetection ModelType = "constrained_object_detection"

	// ModelTypeRegression indicates the model returns a numerical value.
	ModelTypeRegression ModelType = "regression"

	// ModelTypeFreeform indicates the model returns freeform output tensors.
	ModelTypeFreeform ModelType = "freeform"
)

// ImageResizeMode describes how the model resizes input images.
type ImageResizeMode string

const (
	ImageResizeModeNone        ImageResizeMode = "none"
	ImageResizeModeFitShortest ImageResizeMode = "fit-shortest"
	ImageResizeModeFitLongest  ImageResizeMode = "fit-longest"
	ImageResizeModeSquash      ImageResizeMode = "squash"
)

// SensorType describes the source of measurements/values.
type SensorType string

// SensorTypes as source of measurements.
const (
	SensorTypeUnknown       SensorType = "unknown"
	SensorTypeMicrophone    SensorType = "microphone"
	SensorTypeAccelerometer SensorType = "accelerometer"
	SensorTypeCamera        SensorType = "camera"
	SensorTypePositional    SensorType = "positional"
)

// HasAnomalyType indicates the type of anomaly detection used by the model.
type HasAnomalyType int

const (
	HasAnomalyNone            HasAnomalyType = 0
	HasAnomalyKMeans          HasAnomalyType = 1
	HasAnomalyGMM             HasAnomalyType = 2
	HasAnomalyVisualGMM       HasAnomalyType = 3
	HasAnomalyVisualPatchcore HasAnomalyType = 4
)

// InferencingEngine identifies the ML runtime used by the model.
type InferencingEngine int

const (
	InferencingEngineNone         InferencingEngine = 255
	InferencingEngineUtensor      InferencingEngine = 1
	InferencingEngineTflite       InferencingEngine = 2
	InferencingEngineCubeai       InferencingEngine = 3
	InferencingEngineTfliteFull   InferencingEngine = 4
	InferencingEngineTensaiFlow   InferencingEngine = 5
	InferencingEngineTensorRT     InferencingEngine = 6
	InferencingEngineDrpai        InferencingEngine = 7
	InferencingEngineTfliteTidl   InferencingEngine = 8
	InferencingEngineAkida        InferencingEngine = 9
	InferencingEngineSyntiant     InferencingEngine = 10
	InferencingEngineOnnxTidl     InferencingEngine = 11
	InferencingEngineMemryx       InferencingEngine = 12
	InferencingEngineEthosLinux   InferencingEngine = 13
	InferencingEngineAton         InferencingEngine = 14
	InferencingEngineCevaNpn      InferencingEngine = 15
	InferencingEngineNordicAxon   InferencingEngine = 16
	InferencingEngineVlmConnector InferencingEngine = 17
)

// BlockThreshold holds a threshold configuration for a learn block.
// The ID and Type fields identify the block; additional numeric parameters
// are stored in Params.
type BlockThreshold struct {
	ID     int                `json:"id"`
	Type   string             `json:"type"`
	Params map[string]float64 `json:"-"` // flattened into the JSON object
}

// MarshalJSON flattens Params into the same JSON object as ID and Type.
func (b BlockThreshold) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		"id":   b.ID,
		"type": b.Type,
	}
	for k, v := range b.Params {
		m[k] = v
	}
	return json.Marshal(m)
}

// UnmarshalJSON reads a flat JSON object, pulling out id/type and putting
// everything else into Params.
func (b *BlockThreshold) UnmarshalJSON(data []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if v, ok := m["id"]; ok {
		if err := json.Unmarshal(v, &b.ID); err != nil {
			return err
		}
	}
	if v, ok := m["type"]; ok {
		if err := json.Unmarshal(v, &b.Type); err != nil {
			return err
		}
	}
	b.Params = make(map[string]float64)
	for k, raw := range m {
		if k == "id" || k == "type" {
			continue
		}
		var f float64
		if err := json.Unmarshal(raw, &f); err == nil {
			b.Params[k] = f
		}
	}
	return nil
}

// ModelParameters holds the model parameters for a model.
type ModelParameters struct {
	ModelType  ModelType  `json:"model_type"`
	Sensor     int64      `json:"sensor"`
	SensorType SensorType `json:"-"` // derived from Sensor
	IntervalMS float64    `json:"interval_ms"`
	Frequency  float64    `json:"frequency"`

	InputFeaturesCount int `json:"input_features_count"`
	AxisCount          int `json:"axis_count"`

	// Continuous classification.
	SliceSize         int  `json:"slice_size"`
	UseContinuousMode bool `json:"use_continuous_mode"`

	// For images.
	ImageInputHeight  int             `json:"image_input_height"`
	ImageInputWidth   int             `json:"image_input_width"`
	ImageInputFrames  int             `json:"image_input_frames"`
	ImageChannelCount int             `json:"image_channel_count"`
	ImageResizeMode   ImageResizeMode `json:"image_resize_mode,omitempty"`

	// Labels in resulting classifications.
	Labels     []string `json:"labels"`
	LabelCount int      `json:"label_count"`

	HasAnomaly          HasAnomalyType    `json:"has_anomaly"`
	HasObjectTracking   bool              `json:"has_object_tracking,omitempty"`
	HasPerformanceCalib bool              `json:"has_performance_calibration"`
	InferencingEngine   InferencingEngine `json:"inferencing_engine"`
	Thresholds          []BlockThreshold  `json:"thresholds,omitempty"`
	VLMModelDownloadURL string            `json:"vlm_model_download_url,omitempty"`
}

// String returns a human-readable summary of the model parameters.
func (p ModelParameters) String() string {
	var s string
	switch p.SensorType {
	case SensorTypeMicrophone:
		s = fmt.Sprintf("microphone, frequency %vHz, window length %v", p.Frequency, time.Duration(float64(p.InputFeaturesCount)/p.Frequency)*time.Second)
	case SensorTypeAccelerometer:
		axes := p.AxisCount
		if axes == 0 {
			axes = 3
		}
		s = fmt.Sprintf("accelerometer, frequency %vHz, window length %v", p.Frequency, time.Duration(float64(p.InputFeaturesCount)/p.Frequency/float64(axes))*time.Second)
	case SensorTypeCamera:
		s = fmt.Sprintf("camera, %dx%d (%d channels)", p.ImageInputWidth, p.ImageInputHeight, p.ImageChannelCount)
	default:
		s = fmt.Sprintf("model type %s, sensor type %s (%d)", p.ModelType, p.SensorType, p.Sensor)
	}
	if len(p.Labels) > 0 {
		s += ", classes " + strings.Join(p.Labels, ",")
	}
	if p.UseContinuousMode {
		s += fmt.Sprintf(", continuous (slice=%d)", p.SliceSize)
	}
	return s
}

// Project holds the project information stored in the model, originally from
// Edge Impulse Studio.
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

// RunnerClassifyContinuousRequest is a request to classify a slice of data
// in continuous (sliding window) mode. Pass SliceSize features, not
// InputFeaturesCount.
type RunnerClassifyContinuousRequest struct {
	ID                 int64     `json:"id"`
	ClassifyContinuous []float64 `json:"classify_continuous"`
}

// BoundingBox represents a detected object in an image.
type BoundingBox struct {
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
}

// ObjectTrackingResult is a bounding box with an additional object ID for
// tracking across frames.
type ObjectTrackingResult struct {
	ObjectID int     `json:"object_id"`
	Label    string  `json:"label"`
	Value    float64 `json:"value"`
	X        int     `json:"x"`
	Y        int     `json:"y"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
}

// VisualAnomalyCell is a grid cell from a visual anomaly detection result.
type VisualAnomalyCell struct {
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
}

// ClassifyResult holds the output of a single classification call.
type ClassifyResult struct {
	// Classification scores by label (ModelTypeClassification).
	Classification map[string]float64 `json:"classification,omitempty"`

	// BoundingBoxes for detected objects (ModelTypeObjectDetection).
	BoundingBoxes []BoundingBox `json:"bounding_boxes,omitempty"`

	// ObjectTracking adds object IDs to bounding boxes across frames.
	ObjectTracking []ObjectTrackingResult `json:"object_tracking,omitempty"`

	// Regression value (ModelTypeRegression).
	Regression struct {
		Value float64 `json:"value"`
	} `json:"regression,omitempty"`

	// Visual anomaly detection results.
	VisualAnomalyGrid []VisualAnomalyCell `json:"visual_anomaly_grid,omitempty"`
	VisualAnomalyMax  float64             `json:"visual_anomaly_max,omitempty"`
	VisualAnomalyMean float64             `json:"visual_anomaly_mean,omitempty"`

	// Anomaly score (KMeans / GMM anomaly detection).
	Anomaly float64 `json:"anomaly,omitempty"`
}

// ClassifyTiming holds timing information for a classification call.
type ClassifyTiming struct {
	DSP            float64 `json:"dsp"`
	Classification float64 `json:"classification"`
	Anomaly        float64 `json:"anomaly"`
	Postprocessing float64 `json:"postprocessing,omitempty"`
}

// RunnerClassifyResponse is the response from the model to a classify request.
type RunnerClassifyResponse struct {
	RunnerResponse
	Result ClassifyResult `json:"result"`
	Timing ClassifyTiming `json:"timing"`
	// Info contains optional informational messages from the model.
	Info string `json:"info,omitempty"`
}

// String returns a summary of the result.
func (r RunnerClassifyResponse) String() string {
	if !r.Success {
		return fmt.Sprintf("error: %v", r.Error)
	}
	ms := fmt.Sprintf("%dms", int64(r.Timing.Classification))
	var anomaly string
	if r.Result.Anomaly != 0 {
		anomaly = fmt.Sprintf(" anomaly=%.4f", r.Result.Anomaly)
	}
	switch {
	case r.Result.Classification != nil:
		var kv []string
		for k, v := range r.Result.Classification {
			kv = append(kv, fmt.Sprintf("%s=%.4f", k, v))
		}
		sort.Strings(kv)
		return fmt.Sprintf("classification in %s: %s%s", ms, strings.Join(kv, " "), anomaly)
	case r.Result.BoundingBoxes != nil:
		var boxes []string
		for _, b := range r.Result.BoundingBoxes {
			boxes = append(boxes, fmt.Sprintf("x=%d,y=%d,w=%d,h=%d,label=%s,value=%.4f", b.X, b.Y, b.Width, b.Height, b.Label, b.Value))
		}
		return fmt.Sprintf("bounding_boxes in %s: %s%s", ms, strings.Join(boxes, "; "), anomaly)
	case r.Result.Regression.Value != 0:
		return fmt.Sprintf("regression in %s: value=%.4f%s", ms, r.Result.Regression.Value, anomaly)
	case len(r.Result.VisualAnomalyGrid) > 0:
		return fmt.Sprintf("visual_anomaly in %s: max=%.4f mean=%.4f%s", ms, r.Result.VisualAnomalyMax, r.Result.VisualAnomalyMean, anomaly)
	}
	return "(result without classification, bounding boxes, or regression)"
}

// setThresholdRequest is the internal request type for set_threshold.
type setThresholdRequest struct {
	ID           int64          `json:"id"`
	SetThreshold BlockThreshold `json:"set_threshold"`
}

// setThresholdResponse is the response to a set_threshold request.
type setThresholdResponse struct {
	RunnerResponse
}

// setParameterRequest is the internal request type for set_parameter.
type setParameterRequest struct {
	ID           int64          `json:"id"`
	SetParameter map[string]any `json:"set_parameter"`
}

// setParameterResponse is the response to a set_parameter request.
type setParameterResponse struct {
	RunnerResponse
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
// Always call Close on a runner to cleanup any temporary directories.
func NewRunnerProcess(modelPath string, opts *RunnerOpts) (runner *RunnerProcess, rerr error) {
	var err error
	modelPath, err = filepath.Abs(modelPath)
	if err != nil {
		return nil, fmt.Errorf("absolute path for modelPath %q: %w", modelPath, err)
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
			return nil, fmt.Errorf("making temp dir: %w", err)
		}
		r.opts.WorkDir = dir
		r.tempDir = dir
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	cmd := exec.CommandContext(ctx, modelPath, "runner.sock")
	cmd.Dir = r.opts.WorkDir
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting model process: %w", err)
	}
	go cmd.Wait()

	sockPath := filepath.Join(r.opts.WorkDir, "runner.sock")
	for i, delay := 0, 1*time.Millisecond; ; i, delay = i+1, min(delay*2, 100*time.Millisecond) {
		conn, err := net.Dial("unix", sockPath)
		if err == nil {
			r.conn = conn
			break
		}
		if !errors.Is(err, syscall.ENOENT) {
			return nil, fmt.Errorf("opening runner socket: %w", err)
		}
		if i == 100 {
			return nil, fmt.Errorf("no socket from runner after %v", 100*delay)
		}
		time.Sleep(delay)
	}

	helloReq := runnerHelloRequest{ID: r.nextID(), Hello: 1}
	var helloResp runnerHelloResponse
	if err := r.transact(context.Background(), helloReq.ID, helloReq, &helloResp); err != nil {
		return nil, fmt.Errorf("hello to model: %w", err)
	}
	mp := helloResp.ModelParameters
	if string(mp.ModelType) == "" {
		mp.ModelType = ModelTypeClassification
	}
	if mp.ImageInputFrames == 0 {
		mp.ImageInputFrames = 1
	}
	switch mp.Sensor {
	case 1:
		mp.SensorType = SensorTypeMicrophone
	case 2:
		mp.SensorType = SensorTypeAccelerometer
	case 3:
		mp.SensorType = SensorTypeCamera
	case 4:
		mp.SensorType = SensorTypePositional
	default:
		mp.SensorType = SensorTypeUnknown
	}
	r.modelParams = mp
	r.project = helloResp.Project

	return r, nil
}

// transact performs a single request/response exchange with the model process.
// It respects ctx for cancellation; when ctx is done the connection read
// deadline is set to now so the goroutine unblocks promptly.
func (r *RunnerProcess) transact(ctx context.Context, id int64, req any, resp runnerResponser) error {
	resCh := make(chan error, 1)

	go func() {
		if err := json.NewEncoder(r.conn).Encode(req); err != nil {
			resCh <- fmt.Errorf("writing json to model: %w", err)
			return
		}
		r.writeTrace(fmt.Sprintf("%s/runner-%d-request.json", r.opts.TraceDir, id), req)

		dec := json.NewDecoder(r.conn)
		if err := dec.Decode(resp); err != nil {
			resCh <- fmt.Errorf("reading json from model: %w", err)
			return
		}
		r.writeTrace(fmt.Sprintf("%s/runner-%d-response.json", r.opts.TraceDir, id), resp)

		// Model writes a zero byte after the JSON. Drain it.
		buf, err := io.ReadAll(dec.Buffered())
		if err == nil && len(buf) == 0 {
			r.conn.Read([]byte{0}) //nolint:errcheck
		}

		if !resp.runnerResponse().Success {
			resCh <- fmt.Errorf("model error: %s", resp.runnerResponse().Error)
			return
		}
		resCh <- nil
	}()

	select {
	case <-ctx.Done():
		// Unblock the goroutine's blocking Read by setting an immediate deadline.
		r.conn.SetReadDeadline(time.Now()) //nolint:errcheck
		return ctx.Err()
	case err := <-resCh:
		return err
	}
}

func (r *RunnerProcess) writeTrace(filename string, data any) {
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
	return r.lastID.Add(1)
}

// Classify executes the model on the features and returns the classification.
// Deprecated: Use ClassifyWithContext for timeout/cancellation support.
func (r *RunnerProcess) Classify(data []float64) (RunnerClassifyResponse, error) {
	return r.ClassifyWithContext(context.Background(), data)
}

// ClassifyWithContext executes the model on the features and returns the
// classification. The context can be used for timeouts and cancellation.
func (r *RunnerProcess) ClassifyWithContext(ctx context.Context, data []float64) (resp RunnerClassifyResponse, rerr error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	req := RunnerClassifyRequest{
		ID:       r.nextID(),
		Classify: data,
	}
	rerr = r.transact(ctx, req.ID, req, &resp)
	return
}

// ClassifyContinuous classifies a slice of data in continuous (sliding window)
// mode. Pass SliceSize features rather than InputFeaturesCount.
// Deprecated: Use ClassifyContinuousWithContext for timeout/cancellation support.
func (r *RunnerProcess) ClassifyContinuous(data []float64) (RunnerClassifyResponse, error) {
	return r.ClassifyContinuousWithContext(context.Background(), data)
}

// ClassifyContinuousWithContext classifies a slice of data in continuous mode.
// The context can be used for timeouts and cancellation.
// Only valid when ModelParameters.UseContinuousMode is true.
func (r *RunnerProcess) ClassifyContinuousWithContext(ctx context.Context, data []float64) (resp RunnerClassifyResponse, rerr error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	req := RunnerClassifyContinuousRequest{
		ID:                 r.nextID(),
		ClassifyContinuous: data,
	}
	rerr = r.transact(ctx, req.ID, req, &resp)
	return
}

// SetThreshold dynamically updates a learn block's threshold on the running
// model. This allows tuning sensitivity without redeploying.
func (r *RunnerProcess) SetThreshold(ctx context.Context, threshold BlockThreshold) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	req := setThresholdRequest{
		ID:           r.nextID(),
		SetThreshold: threshold,
	}
	var resp setThresholdResponse
	return r.transact(ctx, req.ID, req, &resp)
}

// SetParameter dynamically sets one or more model parameters on the running
// model. Keys and values are model-specific; consult the Edge Impulse
// documentation for your deployment target.
func (r *RunnerProcess) SetParameter(ctx context.Context, params map[string]any) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	req := setParameterRequest{
		ID:           r.nextID(),
		SetParameter: params,
	}
	var resp setParameterResponse
	return r.transact(ctx, req.ID, req, &resp)
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
