package admin

import "net"

// DialTarget returns a host:port suitable for an in-process client to dial,
// given a configured listen address. Inputs of the form "0.0.0.0:N" or "[::]:N"
// are rewritten to "127.0.0.1:N" or "[::1]:N" — a service listening on the
// wildcard is reachable on loopback, and loopback is the TLS SAN that the
// daemon's own cert always covers. Loopback, specific IPs, and bare hostnames
// pass through unchanged. Malformed input (no host:port) passes through so
// callers still see the original value in error messages.
func DialTarget(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return listenAddr
	}
	switch host {
	case "0.0.0.0", "":
		return net.JoinHostPort("127.0.0.1", port)
	case "::":
		return net.JoinHostPort("::1", port)
	}
	return listenAddr
}

// AdvertiseHost picks the host that off-host clients should dial for a service
// listening on listenAddr. If the listen host is a wildcard (0.0.0.0, ::, or
// empty), the caller-supplied advertiseIP is returned; otherwise the listen
// host itself is returned. Port is intentionally dropped — callers append the
// port appropriate for the protocol (some services listen and advertise on
// different ports).
func AdvertiseHost(listenAddr, advertiseIP string) string {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		host = listenAddr
	}
	switch host {
	case "0.0.0.0", "::", "":
		return advertiseIP
	}
	return host
}
