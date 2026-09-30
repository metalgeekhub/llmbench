//go:build windows

package clock

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	qpcProc  = kernel32.NewProc("QueryPerformanceCounter")
	qpfProc  = kernel32.NewProc("QueryPerformanceFrequency")

	freq      int64
	baseCount int64
	baseTime  time.Time
	available bool
)

func init() {
	var f int64
	if r, _, _ := qpfProc.Call(uintptr(unsafe.Pointer(&f))); r == 0 || f <= 0 {
		return
	}
	c, ok := counter()
	if !ok {
		return
	}
	freq, baseCount, baseTime, available = f, c, time.Now(), true
}

func counter() (int64, bool) {
	var c int64
	r, _, _ := qpcProc.Call(uintptr(unsafe.Pointer(&c)))
	return c, r != 0
}

// Now returns the current time with a precise monotonic reading: a fixed
// base time plus the high-resolution counter's elapsed ticks.
func Now() time.Time {
	if !available {
		return time.Now()
	}
	c, ok := counter()
	if !ok {
		return time.Now()
	}
	d := c - baseCount
	elapsed := time.Duration(d/freq)*time.Second + time.Duration(d%freq)*time.Second/time.Duration(freq)
	return baseTime.Add(elapsed)
}
