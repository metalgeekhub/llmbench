package metrics

import (
	"errors"
	"fmt"
)

// SLO holds per-request targets. A request is "good" when it succeeded and
// meets every target that is set. Nil fields are not checked.
type SLO struct {
	TTFTMs       *float64 `json:"ttft_ms,omitempty" yaml:"ttft_ms,omitempty"`               // max time to first token
	TPOTMs       *float64 `json:"tpot_ms,omitempty" yaml:"tpot_ms,omitempty"`               // max time per output token
	E2EMs        *float64 `json:"e2e_ms,omitempty" yaml:"e2e_ms,omitempty"`                 // max end-to-end latency
	MinOutputTPS *float64 `json:"min_output_tps,omitempty" yaml:"min_output_tps,omitempty"` // min output tokens/s per request
}

// Violation reasons, used as keys of Goodput.Violations.
const (
	ViolationError     = "error"
	ViolationTTFT      = "ttft"
	ViolationTPOT      = "tpot"
	ViolationE2E       = "e2e"
	ViolationOutputTPS = "output_tps"
)

// IsZero reports whether no target is set.
func (s SLO) IsZero() bool {
	return s.TTFTMs == nil && s.TPOTMs == nil && s.E2EMs == nil && s.MinOutputTPS == nil
}

// Validate checks that set targets are positive.
func (s SLO) Validate() error {
	for name, v := range map[string]*float64{"TTFT": s.TTFTMs, "TPOT": s.TPOTMs, "end-to-end": s.E2EMs, "output speed": s.MinOutputTPS} {
		if v != nil && *v <= 0 {
			return fmt.Errorf("the %s target must be above 0", name)
		}
	}
	return nil
}

// ErrNoSLO is returned when goodput is requested without any target.
var ErrNoSLO = errors.New("no SLO targets set")

// SLOSample is the part of a measured request that SLOs look at.
type SLOSample struct {
	OK        bool
	TTFTMs    *float64
	TPOTMs    *float64
	E2EMs     float64
	OutputTPS *float64
}

// Goodput is the SLO outcome of a group of requests.
type Goodput struct {
	Requests int `json:"requests"` // completed (ok + failed); canceled excluded
	Good     int `json:"good"`
	// Ratio is Good / Requests (0–1).
	Ratio float64 `json:"ratio"`
	// PerSecond is good requests per second of wall time (0 when unknown).
	PerSecond float64 `json:"per_second"`
	// Violations counts requests failing each criterion (a request can
	// fail several).
	Violations map[string]int `json:"violations"`
}

// Meets reports whether one request satisfies s, and which criteria failed.
func (s SLO) Meets(r SLOSample) (bool, []string) {
	if !r.OK {
		return false, []string{ViolationError}
	}
	var failed []string
	if s.TTFTMs != nil && (r.TTFTMs == nil || *r.TTFTMs > *s.TTFTMs) {
		failed = append(failed, ViolationTTFT)
	}
	// TPOT is undefined for single-token answers; that doesn't violate it.
	if s.TPOTMs != nil && r.TPOTMs != nil && *r.TPOTMs > *s.TPOTMs {
		failed = append(failed, ViolationTPOT)
	}
	if s.E2EMs != nil && r.E2EMs > *s.E2EMs {
		failed = append(failed, ViolationE2E)
	}
	if s.MinOutputTPS != nil && (r.OutputTPS == nil || *r.OutputTPS < *s.MinOutputTPS) {
		failed = append(failed, ViolationOutputTPS)
	}
	return len(failed) == 0, failed
}

// EvaluateGoodput applies s to samples measured over wallSeconds.
func EvaluateGoodput(s SLO, samples []SLOSample, wallSeconds float64) Goodput {
	g := Goodput{Requests: len(samples), Violations: map[string]int{}}
	for _, r := range samples {
		ok, failed := s.Meets(r)
		if ok {
			g.Good++
		}
		for _, f := range failed {
			g.Violations[f]++
		}
	}
	if g.Requests > 0 {
		g.Ratio = float64(g.Good) / float64(g.Requests)
	}
	if wallSeconds > 0 {
		g.PerSecond = float64(g.Good) / wallSeconds
	}
	return g
}
