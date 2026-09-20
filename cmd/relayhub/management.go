package main

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

// Management remains loopback-only; exposing it requires a separate authenticated
// deployment design, not merely an environment override.
func validateManagementAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid management address: %w", err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("invalid management port")
	}
	if host == "localhost" {
		return nil
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return fmt.Errorf("management address must be loopback")
	}
	return nil
}
