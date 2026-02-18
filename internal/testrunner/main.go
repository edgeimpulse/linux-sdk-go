// Command mockrunner simulates an Edge Impulse .eim runner process for testing.
//
// It listens on a Unix socket and responds to the Edge Impulse JSON IPC protocol.
// Configure the model type with -model and enable slow responses with -slow.
//
// Usage:
//
//	mockrunner [flags] <socket_path>
//
// Flags:
//
//	-model string   Model type: classification, regression, object_detection (default "classification")
//	-slow duration  Add artificial delay to classify responses (default 0)
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var (
	modelType = flag.String("model", "classification", "Model type: classification, regression, object_detection")
	slowDelay = flag.Duration("slow", 0, "Artificial delay for classify responses")
)

// ---- Shared protocol types ----

type baseResponse struct {
	ID      int64  `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type modelParameters struct {
	ModelType          string   `json:"model_type"`
	Sensor             int64    `json:"sensor"`
	IntervalMS         float64  `json:"interval_ms"`
	Frequency          float64  `json:"frequency"`
	InputFeaturesCount int      `json:"input_features_count"`
	AxisCount          int      `json:"axis_count"`
	SliceSize          int      `json:"slice_size"`
	UseContinuousMode  bool     `json:"use_continuous_mode"`
	ImageInputHeight   int      `json:"image_input_height"`
	ImageInputWidth    int      `json:"image_input_width"`
	ImageInputFrames   int      `json:"image_input_frames"`
	ImageChannelCount  int      `json:"image_channel_count"`
	Labels             []string `json:"labels"`
	LabelCount         int      `json:"label_count"`
	HasAnomaly         int      `json:"has_anomaly"`
}

type project struct {
	DeployVersion int64  `json:"deploy_version"`
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Owner         string `json:"owner"`
}

type helloResponse struct {
	baseResponse
	ModelParameters modelParameters `json:"model_parameters"`
	Project         project         `json:"project"`
}

type classifyResult struct {
	Classification    map[string]float64 `json:"classification,omitempty"`
	BoundingBoxes     []boundingBox      `json:"bounding_boxes,omitempty"`
	Regression        *regressionResult  `json:"regression,omitempty"`
	VisualAnomalyGrid []anomalyCell      `json:"visual_anomaly_grid,omitempty"`
	VisualAnomalyMax  float64            `json:"visual_anomaly_max,omitempty"`
	VisualAnomalyMean float64            `json:"visual_anomaly_mean,omitempty"`
	Anomaly           float64            `json:"anomaly,omitempty"`
}

type boundingBox struct {
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
}

type regressionResult struct {
	Value float64 `json:"value"`
}

type anomalyCell struct {
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
}

type classifyResponse struct {
	baseResponse
	Result classifyResult `json:"result"`
	Timing struct {
		DSP            float64 `json:"dsp"`
		Classification float64 `json:"classification"`
		Anomaly        float64 `json:"anomaly"`
	} `json:"timing"`
}

// ---- Main ----

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		log.Fatal("Usage: mockrunner [flags] <socket_path>")
	}
	socketPath := args[0]

	// Clean up socket on exit.
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		os.Remove(socketPath)
		os.Exit(0)
	}()

	absSocketPath, err := filepath.Abs(socketPath)
	if err != nil {
		log.Fatalf("abs path: %v", err)
	}

	l, err := net.Listen("unix", absSocketPath)
	if err != nil {
		log.Fatalf("listen error: %v", err)
	}
	defer l.Close()

	log.Printf("Mock runner (%s) listening on %s", *modelType, absSocketPath)

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	for {
		var raw map[string]json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err != io.EOF {
				log.Printf("decode error: %v", err)
			}
			return
		}

		var id int64
		if v, ok := raw["id"]; ok {
			if err := json.Unmarshal(v, &id); err != nil {
				log.Printf("decode id: %v", err)
				return
			}
		}

		switch {
		case raw["hello"] != nil:
			handleHello(enc, conn, id)
		case raw["classify"] != nil:
			handleClassify(enc, conn, id)
		case raw["classify_continuous"] != nil:
			handleClassify(enc, conn, id) // same response shape
		case raw["set_threshold"] != nil:
			handleSetThreshold(enc, conn, id)
		case raw["set_parameter"] != nil:
			handleSetParameter(enc, conn, id)
		default:
			log.Printf("unknown request: %v", raw)
		}
	}
}

func writeResponse(enc *json.Encoder, conn net.Conn, v any) {
	if err := enc.Encode(v); err != nil {
		log.Printf("encode error: %v", err)
	}
	conn.Write([]byte{0}) //nolint:errcheck
}

func handleHello(enc *json.Encoder, conn net.Conn, id int64) {
	params := modelParameters{
		ModelType:          *modelType,
		Sensor:             2, // accelerometer
		IntervalMS:         10,
		Frequency:          100,
		InputFeaturesCount: 33,
		AxisCount:          3,
		SliceSize:          11,
		UseContinuousMode:  true,
		ImageInputFrames:   1,
		HasAnomaly:         0,
	}

	switch *modelType {
	case "classification":
		params.Labels = []string{"idle", "updown", "wave"}
		params.LabelCount = 3
		params.HasAnomaly = 1 // KMeans
	case "regression":
		params.Labels = []string{}
		params.LabelCount = 0
		params.Sensor = 0
	case "object_detection":
		params.Sensor = 3 // camera
		params.ImageInputHeight = 96
		params.ImageInputWidth = 96
		params.ImageChannelCount = 3
		params.Labels = []string{"cat", "dog"}
		params.LabelCount = 2
	}

	resp := helloResponse{
		baseResponse:    baseResponse{ID: id, Success: true},
		ModelParameters: params,
		Project: project{
			DeployVersion: 42,
			ID:            999,
			Name:          "Mock Project",
			Owner:         "tester",
		},
	}
	writeResponse(enc, conn, resp)
}

func handleClassify(enc *json.Encoder, conn net.Conn, id int64) {
	if *slowDelay > 0 {
		time.Sleep(*slowDelay)
	}

	resp := classifyResponse{
		baseResponse: baseResponse{ID: id, Success: true},
	}
	resp.Timing.DSP = 5
	resp.Timing.Classification = 10

	switch *modelType {
	case "classification":
		resp.Result.Classification = map[string]float64{
			"idle":   0.9,
			"updown": 0.05,
			"wave":   0.05,
		}
		resp.Result.Anomaly = 0.12
	case "regression":
		resp.Result.Regression = &regressionResult{Value: 3.14159}
	case "object_detection":
		resp.Result.BoundingBoxes = []boundingBox{
			{Label: "cat", Value: 0.87, X: 10, Y: 20, Width: 50, Height: 60},
		}
	}

	writeResponse(enc, conn, resp)
}

func handleSetThreshold(enc *json.Encoder, conn net.Conn, id int64) {
	resp := baseResponse{ID: id, Success: true}
	writeResponse(enc, conn, resp)
}

func handleSetParameter(enc *json.Encoder, conn net.Conn, id int64) {
	resp := baseResponse{ID: id, Success: true}
	writeResponse(enc, conn, resp)
}
