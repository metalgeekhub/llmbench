// Package clock provides high-resolution timestamps for measurements.
//
// On Windows, Go's monotonic clock advances in coarse steps (~0.5 ms), so
// short intervals such as fast time-to-first-token or inter-chunk gaps can
// read as zero. Now uses QueryPerformanceCounter there; elsewhere it is
// time.Now. Values carry a monotonic reading, so Sub between two of them is
// precise.
package clock
