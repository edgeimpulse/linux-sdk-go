package edgeimpulse

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// buildMockRunner compiles the mock runner binary and returns its path.
// The binary is removed when cleanup is called.
func buildMockRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mockrunner")
	cmd := exec.Command("go", "build", "-o", path, "./internal/testrunner")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mockrunner: %v\n%s", err, out)
	}
	return path
}

// newTestRunner starts a mock runner of the given model type and returns a
// connected RunnerProcess. The runner is closed automatically via t.Cleanup.
func newTestRunner(t *testing.T, mockBin, modelType string, extraArgs ...string) *RunnerProcess {
	t.Helper()

	// Use a short path under /tmp to avoid macOS's 104-char Unix socket limit.
	// t.TempDir() generates paths that are often >100 chars.
	workDir, err := os.MkdirTemp("/tmp", "ei")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(workDir) })

	// Build the mock runner command: mockrunner -model <type> [extra] runner.sock
	// We wrap it in a shell script so we can pass flags.
	args := append([]string{"-model", modelType}, extraArgs...)
	wrapperScript := filepath.Join(workDir, "r.sh")
	scriptContent := "#!/bin/sh\nexec " + mockBin
	for _, a := range args {
		scriptContent += " " + a
	}
	scriptContent += ` "$@"` + "\n"
	if err := os.WriteFile(wrapperScript, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("write wrapper script: %v", err)
	}

	runner, err := NewRunnerProcess(wrapperScript, &RunnerOpts{WorkDir: workDir})
	if err != nil {
		t.Fatalf("NewRunnerProcess(%s): %v", modelType, err)
	}
	t.Cleanup(func() { runner.Close() })
	return runner
}

// ---- Tests ----

func TestHandshake_Classification(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")

	p := r.ModelParameters()
	if p.ModelType != ModelTypeClassification {
		t.Errorf("ModelType = %q, want %q", p.ModelType, ModelTypeClassification)
	}
	if p.SensorType != SensorTypeAccelerometer {
		t.Errorf("SensorType = %q, want %q", p.SensorType, SensorTypeAccelerometer)
	}
	if len(p.Labels) != 3 {
		t.Errorf("Labels len = %d, want 3", len(p.Labels))
	}
	if !p.UseContinuousMode {
		t.Error("UseContinuousMode should be true")
	}
	if p.SliceSize != 11 {
		t.Errorf("SliceSize = %d, want 11", p.SliceSize)
	}
	if p.AxisCount != 3 {
		t.Errorf("AxisCount = %d, want 3", p.AxisCount)
	}
	if p.HasAnomaly != HasAnomalyKMeans {
		t.Errorf("HasAnomaly = %v, want KMeans", p.HasAnomaly)
	}

	proj := r.Project()
	if proj.Name != "Mock Project" {
		t.Errorf("Project.Name = %q, want %q", proj.Name, "Mock Project")
	}
	if proj.DeployVersion != 42 {
		t.Errorf("Project.DeployVersion = %d, want 42", proj.DeployVersion)
	}
}

func TestHandshake_Regression(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "regression")

	p := r.ModelParameters()
	if p.ModelType != ModelTypeRegression {
		t.Errorf("ModelType = %q, want %q", p.ModelType, ModelTypeRegression)
	}
	if p.SensorType != SensorTypeUnknown {
		t.Errorf("SensorType = %q, want %q", p.SensorType, SensorTypeUnknown)
	}
}

func TestHandshake_ObjectDetection(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "object_detection")

	p := r.ModelParameters()
	if p.ModelType != ModelTypeObjectDetection {
		t.Errorf("ModelType = %q, want %q", p.ModelType, ModelTypeObjectDetection)
	}
	if p.SensorType != SensorTypeCamera {
		t.Errorf("SensorType = %q, want %q", p.SensorType, SensorTypeCamera)
	}
	if p.ImageInputWidth != 96 || p.ImageInputHeight != 96 {
		t.Errorf("Image size = %dx%d, want 96x96", p.ImageInputWidth, p.ImageInputHeight)
	}
	if p.ImageInputFrames != 1 {
		t.Errorf("ImageInputFrames = %d, want 1", p.ImageInputFrames)
	}
}

func TestClassify_LegacyAPI(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")

	data := make([]float64, 33)
	resp, err := r.Classify(data)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Classify: not successful")
	}
	if resp.Result.Classification == nil {
		t.Fatal("Classification result is nil")
	}
	if v, ok := resp.Result.Classification["idle"]; !ok || v < 0.5 {
		t.Errorf("Expected idle > 0.5, got %v", v)
	}
	if resp.Result.Anomaly == 0 {
		t.Error("Expected non-zero anomaly score")
	}
	if resp.Timing.Classification == 0 {
		t.Error("Expected non-zero classification timing")
	}
}

func TestClassify_Regression(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "regression")

	resp, err := r.ClassifyWithContext(context.Background(), []float64{1.0, 2.0, 3.0})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if resp.Result.Regression.Value == 0 {
		t.Error("Expected non-zero regression value")
	}
}

func TestClassify_ObjectDetection(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "object_detection")

	resp, err := r.ClassifyWithContext(context.Background(), make([]float64, 96*96*3))
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(resp.Result.BoundingBoxes) == 0 {
		t.Fatal("Expected bounding boxes, got none")
	}
	bb := resp.Result.BoundingBoxes[0]
	if bb.Label != "cat" {
		t.Errorf("BoundingBox label = %q, want %q", bb.Label, "cat")
	}
	if bb.Value < 0.5 {
		t.Errorf("BoundingBox value = %.2f, want >= 0.5", bb.Value)
	}
}

func TestClassifyWithContext_Cancelled(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	_, err := r.ClassifyWithContext(ctx, make([]float64, 33))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestClassifyWithContext_Timeout(t *testing.T) {
	bin := buildMockRunner(t)
	// Use -slow flag to make the mock delay longer than our timeout.
	r := newTestRunner(t, bin, "classification", "-slow", "200ms")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := r.ClassifyWithContext(ctx, make([]float64, 33))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
}

func TestClassifyContinuous(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")

	p := r.ModelParameters()
	if !p.UseContinuousMode {
		t.Skip("model does not support continuous mode")
	}

	// Pass SliceSize features (not InputFeaturesCount).
	data := make([]float64, p.SliceSize*p.AxisCount)
	resp, err := r.ClassifyContinuous(data)
	if err != nil {
		t.Fatalf("ClassifyContinuous: %v", err)
	}
	if !resp.Success {
		t.Fatalf("ClassifyContinuous: not successful")
	}
	if resp.Result.Classification == nil {
		t.Fatal("ClassifyContinuous: classification result is nil")
	}
}

func TestClassifyContinuousWithContext_Cancelled(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.ClassifyContinuousWithContext(ctx, make([]float64, 11))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestSetThreshold(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")

	threshold := BlockThreshold{
		ID:   0,
		Type: "anomaly_gmm",
		Params: map[string]float64{
			"min_confidence_rating": 0.7,
		},
	}

	err := r.SetThreshold(context.Background(), threshold)
	if err != nil {
		t.Fatalf("SetThreshold: %v", err)
	}
}

func TestSetThreshold_Cancelled(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := r.SetThreshold(ctx, BlockThreshold{ID: 0, Type: "anomaly_gmm"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestSetParameter(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")

	err := r.SetParameter(context.Background(), map[string]any{
		"threshold": 0.7,
		"mode":      "fast",
	})
	if err != nil {
		t.Fatalf("SetParameter: %v", err)
	}
}

func TestSetParameter_Cancelled(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := r.SetParameter(ctx, map[string]any{"threshold": 0.5})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestBlockThreshold_JSON(t *testing.T) {
	// Verify custom MarshalJSON/UnmarshalJSON round-trips correctly.
	original := BlockThreshold{
		ID:   1,
		Type: "anomaly_gmm",
		Params: map[string]float64{
			"min_confidence_rating": 0.6,
			"threshold":             0.3,
		},
	}

	data, err := original.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var decoded BlockThreshold
	if err := decoded.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID: got %d, want %d", decoded.ID, original.ID)
	}
	if decoded.Type != original.Type {
		t.Errorf("Type: got %q, want %q", decoded.Type, original.Type)
	}
	for k, v := range original.Params {
		if decoded.Params[k] != v {
			t.Errorf("Params[%q]: got %v, want %v", k, decoded.Params[k], v)
		}
	}
}

func TestModelParameters_String(t *testing.T) {
	bin := buildMockRunner(t)

	tests := []struct {
		modelType  string
		wantSubstr string
	}{
		{"classification", "accelerometer"},
		{"regression", "regression"},
		{"object_detection", "camera"},
	}

	for _, tt := range tests {
		t.Run(tt.modelType, func(t *testing.T) {
			r := newTestRunner(t, bin, tt.modelType)
			s := r.ModelParameters().String()
			if s == "" {
				t.Error("ModelParameters.String() returned empty string")
			}
			if !containsSubstr(s, tt.wantSubstr) {
				t.Errorf("ModelParameters.String() = %q, want substring %q", s, tt.wantSubstr)
			}
		})
	}
}

func TestRunnerClassifyResponse_String(t *testing.T) {
	bin := buildMockRunner(t)

	tests := []struct {
		modelType  string
		wantSubstr string
	}{
		{"classification", "classification in"},
		{"regression", "regression in"},
		{"object_detection", "bounding_boxes in"},
	}

	for _, tt := range tests {
		t.Run(tt.modelType, func(t *testing.T) {
			r := newTestRunner(t, bin, tt.modelType)
			resp, err := r.Classify(make([]float64, 33))
			if err != nil {
				t.Fatalf("Classify: %v", err)
			}
			s := resp.String()
			if !containsSubstr(s, tt.wantSubstr) {
				t.Errorf("String() = %q, want substring %q", s, tt.wantSubstr)
			}
		})
	}
}

func TestProject_String(t *testing.T) {
	bin := buildMockRunner(t)
	r := newTestRunner(t, bin, "classification")
	s := r.Project().String()
	if s == "" {
		t.Error("Project.String() returned empty string")
	}
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
