package utils

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

// LatencyStats contains latency statistics
type LatencyStats struct {
	P99 time.Duration
	P90 time.Duration
	Max time.Duration
	Min time.Duration
	Avg time.Duration
	N   int
}

// String returns a formatted string of latency stats
func (s LatencyStats) String() string {
	return fmt.Sprintf("N=%d, P99=%v, P90=%v, Max=%v, Min=%v, Avg=%v",
		s.N, s.P99, s.P90, s.Max, s.Min, s.Avg)
}

// ParseAllocIPSucceedLatency parses the latency from AllocIPSucceed event message
// Example message: "Alloc IP 10.186.243.34/16-2001:db8:0000:0000:40ce:fc69:8c7:210a/64 took 37.358159ms"
func ParseAllocIPSucceedLatency(message string) (time.Duration, error) {
	// Regex to match "took XXXms" or "took XXXs" or "took XXX.XXXms" etc.
	re := regexp.MustCompile(`took\s+([0-9.]+(?:ns|us|µs|ms|s|m|h))`)
	matches := re.FindStringSubmatch(message)
	if len(matches) < 2 {
		return 0, fmt.Errorf("failed to parse latency from message: %s", message)
	}
	return time.ParseDuration(matches[1])
}

// CalculateLatencyStats calculates latency statistics from a slice of durations
func CalculateLatencyStats(latencies []time.Duration) LatencyStats {
	if len(latencies) == 0 {
		return LatencyStats{}
	}

	// Sort latencies for percentile calculation
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	n := len(sorted)
	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	// Calculate percentiles
	p99Index := int(float64(n) * 0.99)
	p90Index := int(float64(n) * 0.90)
	if p99Index >= n {
		p99Index = n - 1
	}
	if p90Index >= n {
		p90Index = n - 1
	}

	return LatencyStats{
		P99: sorted[p99Index],
		P90: sorted[p90Index],
		Max: sorted[n-1],
		Min: sorted[0],
		Avg: total / time.Duration(n),
		N:   n,
	}
}
