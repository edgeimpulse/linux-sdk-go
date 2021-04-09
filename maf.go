package edgeimpulse

import (
	"fmt"
)

type labelState struct {
	index  int
	sum    float64
	values []float64
}

// MAF is a moving average filter, for smoothing out classification values.
type MAF struct {
	state map[string]*labelState
}

// NewMAF returns a new moving average filter with a history of given size.
// Values are initialized to all zeroes.
func NewMAF(size int, labels []string) (*MAF, error) {
	if size == 0 {
		return nil, fmt.Errorf("size must be > 0")
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("must specify at least one label")
	}
	maf := &MAF{
		state: map[string]*labelState{},
	}
	for _, label := range labels {
		maf.state[label] = &labelState{0, 0, make([]float64, size)}
	}
	return maf, nil
}

// Update adds one classification result to the moving average filter.
// Update returns the smoothed values based on the history.
// Unknown keys (labels) in classification result in an error, as does an empty classification.
func (m *MAF) Update(classification map[string]float64) (map[string]float64, error) {
	if m.state == nil {
		return nil, fmt.Errorf("invalid MAF, use NewMAF")
	}
	if len(classification) == 0 {
		return nil, fmt.Errorf("classification must not be empty")
	}

	// todo: check that all labels from initialization are present?
	// todo: for the first "size" updates, only take present values into account?

	r := map[string]float64{}
	for label, value := range classification {
		ls, ok := m.state[label]
		if !ok {
			return nil, fmt.Errorf("unknown label %q", label)
		}
		ls.sum -= ls.values[ls.index]
		ls.sum += value
		ls.values[ls.index] = value
		r[label] = ls.sum / float64(len(ls.values))
		ls.index++
		if ls.index >= len(ls.values) {
			ls.index = 0
		}
	}
	return r, nil
}
