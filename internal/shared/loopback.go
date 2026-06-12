package shared

import "net"

// IsLoopbackAddr reports whether listenAddr binds exclusively to loopback.
// Addresses with an empty or wildcard host (e.g. ":8080", "0.0.0.0:8080",
// "[::]:8080") bind on all interfaces and return false.
func IsLoopbackAddr(listenAddr string) bool {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	// hostname case (e.g. "localhost")
	addrs, err := net.LookupHost(host)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if !net.ParseIP(a).IsLoopback() {
			return false
		}
	}
	return len(addrs) > 0
}
