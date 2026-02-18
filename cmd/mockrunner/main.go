package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// Request/Response types matching runner.go
type RunnerHelloRequest struct {
	ID    int64 `json:"id"`
	Hello int   `json:"hello"`
}

type RunnerResponse struct {
	ID      int64  `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type ModelParameters struct {
	ModelType  string   `json:"model_type"`
	Sensor     int64    `json:"sensor"`
	IntervalMS float64  `json:"interval_ms"`
	Frequency  float64  `json:"frequency"`
	InputFeat  int      `json:"input_features_count"`
	Labels     []string `json:"labels"`
}

type Project struct {
	DeployVersion int64  `json:"deploy_version"`
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Owner         string `json:"owner"`
}

type RunnerHelloResponse struct {
	RunnerResponse
	ModelParameters ModelParameters `json:"model_parameters"`
	Project         Project         `json:"project"`
}

type RunnerClassifyRequest struct {
	ID       int64     `json:"id"`
	Classify []float64 `json:"classify"`
}

type RunnerClassifyResponse struct {
	RunnerResponse
	Result struct {
		Classification map[string]float64 `json:"classification,omitempty"`
		Regression     struct {
			Value float64 `json:"value"`
		} `json:"regression,omitempty"`
	} `json:"result"`
	Timing struct {
		DSP            float64 `json:"dsp"`
		Classification float64 `json:"classification"`
		Anomaly        float64 `json:"anomaly"`
	} `json:"timing"`
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: mockrunner <socket_path>")
	}
	socketPath := os.Args[1]

	// Ensure we clean up socket on exit
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		os.Remove(socketPath)
		os.Exit(1)
	}()

	// Determine absolute path to socket
	absSocketPath, err := filepath.Abs(socketPath)
	if err != nil {
		log.Fatalf("abs path: %v", err)
	}

	// Listen on unix socket
	l, err := net.Listen("unix", absSocketPath)
	if err != nil {
		log.Fatalf("listen error: %v", err)
	}
	defer l.Close()

	log.Printf("Mock runner listening on %s", absSocketPath)

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
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var raw map[string]interface{}
		if err := decoder.Decode(&raw); err != nil {
			if err != io.EOF {
				log.Printf("decode error: %v", err)
			}
			return
		}

		id := int64(raw["id"].(float64))

		if _, ok := raw["hello"]; ok {
			// Handle Hello
			resp := RunnerHelloResponse{
				RunnerResponse: RunnerResponse{ID: id, Success: true},
				ModelParameters: ModelParameters{
					ModelType:  "regression",
					Sensor:     0,
					IntervalMS: 10,
					Frequency:  100,
					InputFeat:  3,
					Labels:     []string{}, // Regression has no labels usually
				},
				Project: Project{
					DeployVersion: 1,
					ID:            123,
					Name:          "Mock Project",
					Owner:         "MockUser",
				},
			}
			if err := encoder.Encode(resp); err != nil {
				log.Printf("encode hello error: %v", err)
				return
			}
			// Write zero byte delimiter
			conn.Write([]byte{0})
		} else if _, ok := raw["classify"]; ok {
			// Handle Classify
			resp := RunnerClassifyResponse{
				RunnerResponse: RunnerResponse{ID: id, Success: true},
			}

			// Mock a regression response
			resp.Result.Regression.Value = 3.14159
			resp.Timing.Classification = 10

			if err := encoder.Encode(resp); err != nil {
				log.Printf("encode classify error: %v", err)
				return
			}
			// Write zero byte delimiter
			conn.Write([]byte{0})
		}
	}
}
