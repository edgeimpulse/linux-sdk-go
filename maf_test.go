package edgeimpulse_test

import (
	"testing"

	edgeimpulse "github.com/edgeimpulse/linux-sdk-go"
)

func TestMAF(t *testing.T) {
	m0 := &edgeimpulse.MAF{}
	_, err := m0.Update(map[string]float64{"a": 1.5})
	if err == nil {
		t.Errorf("missing error for MAF created without NewMAF")
	}

	m0, err = edgeimpulse.NewMAF(3, []string{"a", "b"})
	if err != nil {
		t.Fatalf("making new MAF: %v", err)
	}

	r, err := m0.Update(map[string]float64{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if r["a"] != 1.0/3 || r["b"] != 2.0/3 {
		t.Fatalf("unexpected result after Update: %v", r)
	}
	r, _ = m0.Update(map[string]float64{"a": 1, "b": 2})
	if r["a"] != 2.0/3 || r["b"] != 4.0/3 {
		t.Fatalf("unexpected result after Update: %v", r)
	}
	r, _ = m0.Update(map[string]float64{"a": 1, "b": 2})
	if r["a"] != 3.0/3 || r["b"] != 6.0/3 {
		t.Fatalf("unexpected result after Update: %v", r)
	}
	r, _ = m0.Update(map[string]float64{"a": 1, "b": 2})
	if r["a"] != 3.0/3 || r["b"] != 6.0/3 {
		t.Fatalf("unexpected result after Update: %v", r)
	}

	_, err = m0.Update(nil)
	if err == nil {
		t.Fatalf("missing error for nil update")
	}

	_, err = m0.Update(map[string]float64{"c": 1, "d": 2})
	if err == nil {
		t.Fatalf("missing error for unknown labels")
	}

	_, err = edgeimpulse.NewMAF(0, []string{"a"})
	if err == nil {
		t.Fatalf("missing error for new MAF with size 0")
	}

	_, err = edgeimpulse.NewMAF(3, []string{})
	if err == nil {
		t.Fatalf("missing error for new MAF without labels")
	}
}
