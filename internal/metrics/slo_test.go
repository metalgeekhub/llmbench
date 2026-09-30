package metrics

import (
	"reflect"
	"testing"
)

func f(v float64) *float64 { return &v }

func TestSLOMeets(t *testing.T) {
	slo := SLO{TTFTMs: f(500), TPOTMs: f(50), E2EMs: f(3000), MinOutputTPS: f(20)}
	good := SLOSample{OK: true, TTFTMs: f(200), TPOTMs: f(30), E2EMs: 1500, OutputTPS: f(33)}
	tests := []struct {
		name   string
		sample SLOSample
		ok     bool
		failed []string
	}{
		{"all targets met", good, true, nil},
		{"failed request", SLOSample{OK: false}, false, []string{ViolationError}},
		{"slow first token", SLOSample{OK: true, TTFTMs: f(900), TPOTMs: f(30), E2EMs: 1500, OutputTPS: f(33)}, false, []string{ViolationTTFT}},
		{"no first token", SLOSample{OK: true, E2EMs: 10, OutputTPS: f(33)}, false, []string{ViolationTTFT}},
		{"slow decode", SLOSample{OK: true, TTFTMs: f(200), TPOTMs: f(80), E2EMs: 4000, OutputTPS: f(12)}, false, []string{ViolationTPOT, ViolationE2E, ViolationOutputTPS}},
		{"single token (no TPOT)", SLOSample{OK: true, TTFTMs: f(200), E2EMs: 210, OutputTPS: f(25)}, true, nil},
		{"boundary values pass", SLOSample{OK: true, TTFTMs: f(500), TPOTMs: f(50), E2EMs: 3000, OutputTPS: f(20)}, true, nil},
	}
	for _, tt := range tests {
		ok, failed := slo.Meets(tt.sample)
		if ok != tt.ok || !reflect.DeepEqual(failed, tt.failed) {
			t.Errorf("%s: Meets = %v %v, want %v %v", tt.name, ok, failed, tt.ok, tt.failed)
		}
	}
}

func TestEvaluateGoodput(t *testing.T) {
	slo := SLO{TTFTMs: f(500)}
	samples := []SLOSample{
		{OK: true, TTFTMs: f(100)},
		{OK: true, TTFTMs: f(200)},
		{OK: true, TTFTMs: f(900)},
		{OK: false},
	}
	g := EvaluateGoodput(slo, samples, 2)
	if g.Requests != 4 || g.Good != 2 || g.Ratio != 0.5 || g.PerSecond != 1 {
		t.Errorf("goodput = %+v", g)
	}
	if g.Violations[ViolationTTFT] != 1 || g.Violations[ViolationError] != 1 {
		t.Errorf("violations = %v", g.Violations)
	}
	if e := EvaluateGoodput(slo, nil, 0); e.Ratio != 0 || e.PerSecond != 0 || e.Violations == nil {
		t.Errorf("empty goodput = %+v", e)
	}
}

func TestSLOValidate(t *testing.T) {
	if !(SLO{}).IsZero() || (SLO{TTFTMs: f(1)}).IsZero() {
		t.Error("IsZero")
	}
	if err := (SLO{TTFTMs: f(0)}).Validate(); err == nil {
		t.Error("zero target should fail")
	}
	if err := (SLO{E2EMs: f(-1)}).Validate(); err == nil {
		t.Error("negative target should fail")
	}
	if err := (SLO{TTFTMs: f(500), MinOutputTPS: f(10)}).Validate(); err != nil {
		t.Error(err)
	}
}
