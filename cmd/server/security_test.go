package main

import "testing"

func TestIsLoopbackHost(t *testing.T) {
	for _, host := range []string{"localhost", "LOCALHOST", "127.0.0.1", "::1", "[::1]"} {
		if !isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"0.0.0.0", "192.0.2.1", "relay.example.com", ""} {
		if isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = true, want false", host)
		}
	}
}

func TestValidateRelaySecurity(t *testing.T) {
	tests := []struct {
		name, host string
		keys       bool
		cert, key  string
		wantErr    bool
	}{
		{"loopback development", "127.0.0.1", true, "", "", false},
		{"loopback legacy migration", "localhost", false, "", "", false},
		{"public secure", "0.0.0.0", true, "cert.pem", "key.pem", false},
		{"public without keys", "0.0.0.0", false, "cert.pem", "key.pem", true},
		{"public without TLS", "0.0.0.0", true, "", "", true},
		{"partial TLS config", "127.0.0.1", true, "cert.pem", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRelaySecurity(tt.host, tt.keys, tt.cert, tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRelaySecurity() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
