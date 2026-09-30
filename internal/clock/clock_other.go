//go:build !windows

package clock

import "time"

// Now returns the current time with a precise monotonic reading.
func Now() time.Time { return time.Now() }
