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

// validateListenAddress checks a data-plane (proxy/gateway) listen address.
// Any IP host, "localhost" or an empty host (all interfaces) is accepted with a
// usable port; the runtime logs a warning when the address is reachable beyond
// loopback. Only the management plane is restricted to loopback.
func validateListenAddress(name, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid %s address: %w", name, err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("invalid %s port", name)
	}
	if host == "" || host == "localhost" {
		return nil
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return fmt.Errorf("invalid %s host: must be an IP address, localhost or empty", name)
	}
	return nil
}
