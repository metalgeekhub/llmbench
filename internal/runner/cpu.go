package runner

import (
	"runtime"
	rtmetrics "runtime/metrics"
)

// cpuSampler estimates this process's CPU utilisation (share of GOMAXPROCS
// capacity) between start and stop, so the UI can warn when the load
// generator itself may be the bottleneck.
type cpuSampler struct {
	total, idle float64
}

func readCPU() (total, idle float64) {
	// The runtime updates CPU class estimates at GC boundaries; a GC here
	// (only at cell start/end) makes the reading current.
	runtime.GC()
	s := []rtmetrics.Sample{
		{Name: "/cpu/classes/total:cpu-seconds"},
		{Name: "/cpu/classes/idle:cpu-seconds"},
	}
	rtmetrics.Read(s)
	if s[0].Value.Kind() != rtmetrics.KindFloat64 || s[1].Value.Kind() != rtmetrics.KindFloat64 {
		return 0, 0
	}
	return s[0].Value.Float64(), s[1].Value.Float64()
}

func startCPU() cpuSampler {
	t, i := readCPU()
	return cpuSampler{total: t, idle: i}
}

// stop returns utilisation in percent, or nil if it could not be measured.
func (c cpuSampler) stop() *float64 {
	t, i := readCPU()
	dt := t - c.total
	if dt <= 0 {
		return nil
	}
	pct := 100 * (1 - (i-c.idle)/dt)
	pct = max(0, min(100, pct))
	return &pct
}
