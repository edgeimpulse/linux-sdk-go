//go:build integration

// Integration tests for the Edge Impulse Linux SDK for Go.
//
// These tests require a real .eim model file. Download one from your Edge Impulse
// project (Deployment page → Linux target) or via the CLI:
//
//	edge-impulse-linux-runner --download modelfile.eim
//
// Run with:
//
//	EIM_PATH=/path/to/modelfile.eim go test -tags integration -v -timeout 60s .
package edgeimpulse

import (
	"context"
	"os"
	"testing"
	"time"
)

// getEIMPath returns the path to the .eim model file from the EIM_PATH
// environment variable, or skips the test if not set.
func getEIMPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("EIM_PATH")
	if path == "" {
		t.Skip("EIM_PATH not set; skipping integration test. " +
			"Set EIM_PATH=/path/to/model.eim to run against a real model.")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("EIM_PATH %q does not exist: %v", path, err)
	}
	return path
}

func TestIntegration_Handshake(t *testing.T) {
	path := getEIMPath(t)

	runner, err := NewRunnerProcess(path, nil)
	if err != nil {
		t.Fatalf("NewRunnerProcess: %v", err)
	}
	defer runner.Close()

	p := runner.ModelParameters()
	t.Logf("Model: %s", p)
	t.Logf("Project: %s", runner.Project())

	if p.ModelType == "" {
		t.Error("ModelType is empty")
	}
	if p.InputFeaturesCount == 0 {
		t.Error("InputFeaturesCount is 0")
	}
	if runner.Project().Name == "" {
		t.Error("Project.Name is empty")
	}
}

func TestIntegration_Classify(t *testing.T) {
	path := getEIMPath(t)

	runner, err := NewRunnerProcess(path, nil)
	if err != nil {
		t.Fatalf("NewRunnerProcess: %v", err)
	}
	defer runner.Close()

	p := runner.ModelParameters()
	t.Logf("Model: %s", p)

	// Use zero-padded features — valid for any model type.
	data := make([]float64, p.InputFeaturesCount)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := runner.ClassifyWithContext(ctx, data)
	if err != nil {
		t.Fatalf("ClassifyWithContext: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Classify not successful: %s", resp.Error)
	}

	t.Logf("Result: %s", resp)

	// Validate result shape matches model type.
	switch p.ModelType {
	case ModelTypeClassification:
		if resp.Result.Classification == nil {
			t.Error("Expected Classification result, got nil")
		}
		if len(resp.Result.Classification) != len(p.Labels) {
			t.Errorf("Classification has %d entries, want %d (labels: %v)",
				len(resp.Result.Classification), len(p.Labels), p.Labels)
		}
	case ModelTypeObjectDetection, ModelTypeConstrainedObjectDetection:
		// BoundingBoxes may be empty if nothing detected — that's valid.
		t.Logf("Bounding boxes: %d", len(resp.Result.BoundingBoxes))
	case ModelTypeRegression:
		t.Logf("Regression value: %v", resp.Result.Regression.Value)
	}

	if resp.Timing.DSP == 0 && resp.Timing.Classification == 0 {
		t.Error("Both DSP and Classification timing are 0 — suspicious")
	}
}

func TestIntegration_ClassifyContinuous(t *testing.T) {
	path := getEIMPath(t)

	runner, err := NewRunnerProcess(path, nil)
	if err != nil {
		t.Fatalf("NewRunnerProcess: %v", err)
	}
	defer runner.Close()

	p := runner.ModelParameters()
	if !p.UseContinuousMode {
		t.Skip("Model does not support continuous mode (UseContinuousMode=false)")
	}
	if p.SliceSize == 0 {
		t.Skip("Model SliceSize is 0, cannot test continuous mode")
	}

	t.Logf("Model: %s (slice_size=%d)", p, p.SliceSize)

	axes := p.AxisCount
	if axes == 0 {
		axes = 1
	}
	data := make([]float64, p.SliceSize*axes)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := runner.ClassifyContinuousWithContext(ctx, data)
	if err != nil {
		t.Fatalf("ClassifyContinuousWithContext: %v", err)
	}
	if !resp.Success {
		t.Fatalf("ClassifyContinuous not successful: %s", resp.Error)
	}
	t.Logf("Continuous result: %s", resp)
}

func TestIntegration_SetThreshold(t *testing.T) {
	path := getEIMPath(t)

	runner, err := NewRunnerProcess(path, nil)
	if err != nil {
		t.Fatalf("NewRunnerProcess: %v", err)
	}
	defer runner.Close()

	p := runner.ModelParameters()
	if len(p.Thresholds) == 0 {
		t.Skip("Model has no thresholds to set")
	}

	// Use the first threshold from the model as a template.
	threshold := p.Thresholds[0]
	t.Logf("Setting threshold: id=%d type=%s", threshold.ID, threshold.Type)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := runner.SetThreshold(ctx, threshold); err != nil {
		t.Fatalf("SetThreshold: %v", err)
	}
}

func TestIntegration_ContextTimeout(t *testing.T) {
	path := getEIMPath(t)

	runner, err := NewRunnerProcess(path, nil)
	if err != nil {
		t.Fatalf("NewRunnerProcess: %v", err)
	}
	defer runner.Close()

	// A 1ns timeout should always expire before the model responds.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	_, err = runner.ClassifyWithContext(ctx, make([]float64, runner.ModelParameters().InputFeaturesCount))
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	t.Logf("Got expected error: %v", err)
}
