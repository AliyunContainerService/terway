package node

import (
	"testing"
)

func TestPodIPs(t *testing.T) {
	tests := []struct {
		name     string
		ips      []string
		wantIPv4 string
		wantIPv6 string
		wantErr  bool
	}{
		{
			name:     "Empty slice",
			ips:      []string{},
			wantIPv4: "",
			wantIPv6: "",
			wantErr:  false,
		},
		{
			name:     "Only IPv4",
			ips:      []string{"192.168.1.1"},
			wantIPv4: "192.168.1.1",
			wantIPv6: "",
			wantErr:  false,
		},
		{
			name:     "Only IPv6",
			ips:      []string{"2001:db8::1"},
			wantIPv4: "",
			wantIPv6: "2001:db8::1",
			wantErr:  false,
		},
		{
			name:     "Mixed IPv4 and IPv6",
			ips:      []string{"192.168.1.1", "2001:db8::1"},
			wantIPv4: "192.168.1.1",
			wantIPv6: "2001:db8::1",
			wantErr:  false,
		},
		{
			name:     "Invalid IP address",
			ips:      []string{"192.168.1", "2001:db8::1"},
			wantIPv4: "",
			wantIPv6: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIPv4, gotIPv6, err := podIPs(tt.ips)
			if (err != nil) != tt.wantErr {
				t.Errorf("podIPs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotIPv4 != tt.wantIPv4 {
				t.Errorf("podIPs() gotIPv4 = %v, want %v", gotIPv4, tt.wantIPv4)
			}
			if gotIPv6 != tt.wantIPv6 {
				t.Errorf("podIPs() gotIPv6 = %v, want %v", gotIPv6, tt.wantIPv6)
			}
		})
	}
}
