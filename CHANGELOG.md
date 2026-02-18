# Changelog

## v1.0.0 — 2026-02-18

**Requires Go 1.23+.** Users on Go 1.15 can pin to `v0.1.0` (the legacy tag)
which preserves the original SDK without any of these changes.

First tagged release. Modernizes the SDK to Go 1.23 and brings feature parity
with the Node.js reference implementation.

### New features

- **`SetParameter`** — set arbitrary model parameters at runtime via the
  `set_parameter` protocol message (key/value pairs, model-specific).
- **`ClassifyWithContext`** — context-aware classify with cancellation and
  timeout support. The original `Classify` is preserved for backward
  compatibility and delegates to this method.
- **`ClassifyContinuous` / `ClassifyContinuousWithContext`** — sliding-window
  classification for audio and other continuous models. Sends `SliceSize`
  features per call using the `classify_continuous` protocol message.
- **`SetThreshold`** — dynamically tune learn-block thresholds on a running
  model without redeploying (`set_threshold` protocol message).
- **Regression model support** — `ModelTypeRegression` constant and
  `Result.Regression.Value` field in `RunnerClassifyResponse`.
- **Extended `ModelParameters`** — `AxisCount`, `SliceSize`,
  `UseContinuousMode`, `ImageInputFrames`, `ImageResizeMode` (typed enum),
  `HasPerformanceCalib`, `InferencingEngine`, `Thresholds`,
  `HasObjectTracking`, `VLMModelDownloadURL`.
- **`ImageResizeMode` typed enum** — `none`, `fit-shortest`, `fit-longest`,
  `squash` (was a plain `string`).
- **Typed enums** — `HasAnomalyType` (`None`, `KMeans`, `GMM`, `VisualGMM`,
  `VisualPatchcore`) and `InferencingEngine` (17 values matching Node.js SDK).
- **`BlockThreshold`** — with custom `MarshalJSON`/`UnmarshalJSON` to flatten
  dynamic params into the wire format.
- **Extended result fields** — `ObjectTracking`, `VisualAnomalyGrid`,
  `VisualAnomalyMax`, `VisualAnomalyMean`, `Info`.
- **`positional` sensor type** — sensor value 4 now maps to
  `SensorTypePositional` (was `unknown`).
- **`Runner` interface** — `audio` and `image` classifiers now accept the
  `Runner` interface, making them easier to test and mock.

### Bug fixes

- **`image/classifier.go`** — pixel-packing loop was shared between RGB and
  grayscale paths via a single `data` slice allocated outside the `if/else`
  block. Both paths now have explicit, self-contained pixel loops.
- **`audio/classifier.go`** — always called `Classify` with the full
  `InputFeaturesCount` window, even for continuous models. Now uses
  `ClassifyContinuous` with `SliceSize` for continuous models, and resets the
  window after each call for standard models.
- **Context cancellation** — `conn.SetReadDeadline(time.Now())` is now called
  when the context is cancelled, so the background goroutine unblocks promptly
  instead of leaking until the connection closes.

### Modernization

- Go version updated from 1.15 → 1.23.
- `io/ioutil` replaced with `io` / `os` throughout.
- `interface{}` → `any`.
- `%v` → `%w` in all error wrapping.
- `sync/atomic.Int64` for the message ID counter (was a plain `int64`).
- `filepath.Join` for socket and trace paths (was string concatenation).
- `internal/testrunner` — mock runner moved from `cmd/mockrunner` to the
  idiomatic location for internal test infrastructure.

### Testing

- Comprehensive unit test suite (`runner_test.go`) covering all model types
  (classification, regression, object detection), context cancellation/timeout,
  `ClassifyContinuous`, `SetThreshold`, `BlockThreshold` JSON round-trip, and
  `String()` methods.
- Integration test file (`runner_integration_test.go`, build tag `integration`)
  for testing against a real `.eim` model:
  ```
  EIM_PATH=./modelfile.eim go test -tags integration -v -timeout 60s .
  ```

### Removed

- `cmd/eimclassify` — was added during development, not part of the original
  repo.
