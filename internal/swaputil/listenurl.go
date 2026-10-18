package swaputil

import "net"

// FormatListenURL returns listenAddr prefixed with scheme when the address
// has a non-empty host. Addresses that listen on all interfaces, such as
// ":8080", are returned unchanged so startup logs do not show an invalid
// URL like "http://:8080".
func FormatListenURL(scheme, listenAddr string) string {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil || host == "" {
		return listenAddr
	}
	return scheme + "://" + listenAddr
}
