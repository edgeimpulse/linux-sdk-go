# Edge Impulse Linux SDK for Go

Go SDK for running [Edge Impulse](https://edgeimpulse.com) machine learning models on Linux (x86_64, ARM). Models run as a separate `.eim` process; this SDK communicates with them over a Unix socket.

> **Platform note:** `.eim` model files are Linux binaries. This SDK will not work on macOS or Windows.

## Prerequisites

- Go 1.23+
- A trained model exported as a Linux `.eim` file (see below)
- `sox` for audio examples, a V4L2-compatible camera for image examples

## Version compatibility

| Version | Go requirement | Notes |
|---|---|---|
| `v1.0.0`+ | Go 1.23+ | This version — context support, continuous classification, full Node.js parity |
| `v0.1.0` | Go 1.15+ | Legacy version, no new features, bug fixes only |

If you are on Go 1.15 and cannot upgrade, pin to the legacy version in your `go.mod`:

```
require github.com/edgeimpulse/linux-sdk-go v0.1.0
```


## Getting a model

```sh
npm install -g edge-impulse-cli
edge-impulse-linux-runner --download model.eim
# Switching projects? Add --clean
```

Or download directly from your project's **Deployment** page → **Linux (AARCH64 / x86_64)**.

## Installation

```sh
go get github.com/edgeimpulse/linux-sdk-go
```

## Quick start

```go
runner, err := edgeimpulse.NewRunnerProcess("model.eim", nil)
if err != nil {
    log.Fatal(err)
}
defer runner.Close()

log.Printf("project: %s", runner.Project())
log.Printf("model:   %s", runner.ModelParameters())

// Classify a slice of features (length = ModelParameters.InputFeaturesCount)
features := make([]float64, runner.ModelParameters().InputFeaturesCount)
resp, err := runner.ClassifyWithContext(ctx, features)
```

## Moving average filter

For classification models (especially audio), smooth results over a window of N inferences to reduce false positives:

```go
maf, err := edgeimpulse.NewMAF(4, runner.ModelParameters().Labels)

resp, err := runner.ClassifyWithContext(ctx, features)
smoothed, err := maf.Update(resp.Result.Classification)
// smoothed is a map[string]float64 averaged over the last 4 results
```

## Model types

| `ModelType` | Result field |
|---|---|
| `classification` | `Result.Classification` — map of label → score |
| `object_detection` / `constrained_object_detection` | `Result.BoundingBoxes` |
| `regression` | `Result.Regression.Value` |

## Continuous (sliding-window) models

Audio and some sensor models use a sliding window. Pass `SliceSize` features per call instead of the full window:

```go
p := runner.ModelParameters()
if p.UseContinuousMode {
    slice := make([]float64, p.SliceSize*p.AxisCount)
    // fill slice with latest sensor data...
    resp, err := runner.ClassifyContinuousWithContext(ctx, slice)
}
```

## Adjusting thresholds at runtime

```go
// Available thresholds are listed in runner.ModelParameters().Thresholds
err := runner.SetThreshold(ctx, edgeimpulse.BlockThreshold{
    ID:   0,
    Type: "anomaly_gmm",
    Params: map[string]float64{"min_confidence_rating": 0.7},
})
```

## Examples

| Command | Description |
|---|---|
| `cmd/eimaudio` | Microphone → classify in real time |
| `cmd/eimimage` | Webcam → classify in real time |
| `cmd/eimcollect` | Collect sensor data and upload to Edge Impulse |

Build any example:

```sh
cd cmd/eimaudio && go build && ./eimaudio model.eim
```

## Collecting data

To collect data from custom sensors and upload to Edge Impulse for training, see [`cmd/eimcollect`](cmd/eimcollect/main.go).

## Testing

```sh
# Unit tests (no hardware required)
go test ./...

# Integration tests against a real model
EIM_PATH=./model.eim go test -tags integration -v -timeout 60s .

# Integration tests using Docker (Apple Silicon):
docker run --platform linux/amd64 --rm -v $(pwd):/sdk -v ./model.eim:/model.eim golang:1.23 \
  bash -c "cd /sdk && EIM_PATH=/model.eim go test -tags integration -v ."
```

Integration tests auto-skip features the model doesn't support (e.g. `ClassifyContinuous` on a non-continuous model).
