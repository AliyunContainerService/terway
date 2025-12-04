package utils

import (
	"strings"
	"testing"
	"time"
)

func TestParseAllocIPSucceedLatency(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected time.Duration
		wantErr  bool
	}{
		{
			name:     "milliseconds",
			message:  "Alloc IP 10.186.243.34/16-2001:db8:0000:0000:40ce:fc69:8c7:210a/64 took 37.358159ms",
			expected: 37358159 * time.Nanosecond, // 37.358159ms
			wantErr:  false,
		},
		{
			name:     "seconds",
			message:  "Alloc IP 10.186.243.124/16 took 1.5s",
			expected: 1500 * time.Millisecond,
			wantErr:  false,
		},
		{
			name:     "microseconds",
			message:  "Alloc IP 10.0.0.1/24 took 500us",
			expected: 500 * time.Microsecond,
			wantErr:  false,
		},
		{
			name:     "nanoseconds",
			message:  "Alloc IP 10.0.0.1/24 took 1000000ns",
			expected: 1 * time.Millisecond,
			wantErr:  false,
		},
		{
			name:     "integer milliseconds",
			message:  "Alloc IP 10.186.243.124/16 took 724ms",
			expected: 724 * time.Millisecond,
			wantErr:  false,
		},
		{
			name:     "complex message with long duration",
			message:  "Alloc IP 10.186.243.124/16-2001:db8:0000:0000:40ce:fc69:8c7:2109/64 took 724.537079ms",
			expected: 724537079 * time.Nanosecond,
			wantErr:  false,
		},
		{
			name:    "no latency info",
			message: "Alloc IP 10.186.243.34/16 success",
			wantErr: true,
		},
		{
			name:    "empty message",
			message: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAllocIPSucceedLatency(tt.message)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseAllocIPSucceedLatency() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("ParseAllocIPSucceedLatency() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCalculateLatencyStats(t *testing.T) {
	tests := []struct {
		name      string
		latencies []time.Duration
		expected  LatencyStats
	}{
		{
			name:      "empty slice",
			latencies: []time.Duration{},
			expected:  LatencyStats{},
		},
		{
			name:      "single value",
			latencies: []time.Duration{100 * time.Millisecond},
			expected: LatencyStats{
				P99: 100 * time.Millisecond,
				P90: 100 * time.Millisecond,
				Max: 100 * time.Millisecond,
				Min: 100 * time.Millisecond,
				Avg: 100 * time.Millisecond,
				N:   1,
			},
		},
		{
			name: "multiple values",
			latencies: []time.Duration{
				10 * time.Millisecond,
				20 * time.Millisecond,
				30 * time.Millisecond,
				40 * time.Millisecond,
				50 * time.Millisecond,
				60 * time.Millisecond,
				70 * time.Millisecond,
				80 * time.Millisecond,
				90 * time.Millisecond,
				100 * time.Millisecond,
			},
			expected: LatencyStats{
				P99: 100 * time.Millisecond,
				P90: 100 * time.Millisecond, // index = int(10 * 0.90) = 9, which is 100ms
				Max: 100 * time.Millisecond,
				Min: 10 * time.Millisecond,
				Avg: 55 * time.Millisecond,
				N:   10,
			},
		},
		{
			name: "unsorted input",
			latencies: []time.Duration{
				50 * time.Millisecond,
				10 * time.Millisecond,
				90 * time.Millisecond,
				30 * time.Millisecond,
				70 * time.Millisecond,
			},
			expected: LatencyStats{
				P99: 90 * time.Millisecond,
				P90: 90 * time.Millisecond,
				Max: 90 * time.Millisecond,
				Min: 10 * time.Millisecond,
				Avg: 50 * time.Millisecond,
				N:   5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateLatencyStats(tt.latencies)
			if got.N != tt.expected.N {
				t.Errorf("N = %d, want %d", got.N, tt.expected.N)
			}
			if got.Min != tt.expected.Min {
				t.Errorf("Min = %v, want %v", got.Min, tt.expected.Min)
			}
			if got.Max != tt.expected.Max {
				t.Errorf("Max = %v, want %v", got.Max, tt.expected.Max)
			}
			if got.Avg != tt.expected.Avg {
				t.Errorf("Avg = %v, want %v", got.Avg, tt.expected.Avg)
			}
			if got.P90 != tt.expected.P90 {
				t.Errorf("P90 = %v, want %v", got.P90, tt.expected.P90)
			}
			if got.P99 != tt.expected.P99 {
				t.Errorf("P99 = %v, want %v", got.P99, tt.expected.P99)
			}
		})
	}
}

func TestLatencyStatsString(t *testing.T) {
	stats := LatencyStats{
		P99: 100 * time.Millisecond,
		P90: 80 * time.Millisecond,
		Max: 120 * time.Millisecond,
		Min: 10 * time.Millisecond,
		Avg: 50 * time.Millisecond,
		N:   100,
	}

	result := stats.String()
	if result == "" {
		t.Error("String() returned empty string")
	}

	// Check that it contains all the expected values
	expectedParts := []string{"N=100", "P99=100ms", "P90=80ms", "Max=120ms", "Min=10ms", "Avg=50ms"}
	for _, part := range expectedParts {
		if !strings.Contains(result, part) {
			t.Errorf("String() = %s, expected to contain %s", result, part)
		}
	}
}
