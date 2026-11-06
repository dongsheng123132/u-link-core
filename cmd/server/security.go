package main

import (
	"fmt"
	"net"
	"strings"
)

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateRelaySecurity(listenHost string, requireDeviceKeys bool, tlsCert, tlsKey string) error {
	if (tlsCert == "") != (tlsKey == "") {
		return fmt.Errorf("TLS certificate and key must be configured together")
	}
	if isLoopbackHost(listenHost) {
		return nil
	}
	if !requireDeviceKeys {
		return fmt.Errorf("refusing non-loopback listen on %q with device key enforcement disabled; enable REQUIRE_DEVICE_KEYS or --require-device-keys", listenHost)
	}
	if tlsCert == "" {
		return fmt.Errorf("refusing non-loopback listen on %q without TLS; configure TLS_CERT and TLS_KEY", listenHost)
	}
	return nil
}
