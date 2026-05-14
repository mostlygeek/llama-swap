package proxy

import (
	"net"
	"os"
	"strconv"

	"github.com/grandcat/zeroconf"
)

const mdnsServiceType = "_llamaswap._tcp"
const mdnsDomain = "local."

// RegisterMDNS advertises this llama-swap instance on the LAN via mDNS/Bonjour
// under the service type _llamaswap._tcp.local. The registration is automatically
// withdrawn when the ProxyManager shuts down.
//
// Call SetListenAddr before RegisterMDNS so the correct port is announced.
func (pm *ProxyManager) RegisterMDNS() {
	if pm.listenAddr == "" {
		return
	}
	_, portStr, err := net.SplitHostPort(pm.listenAddr)
	if err != nil {
		pm.proxyLogger.Warnf("mDNS: cannot parse listen addr %q: %v", pm.listenAddr, err)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		pm.proxyLogger.Warnf("mDNS: invalid port in %q", pm.listenAddr)
		return
	}

	hostname, _ := os.Hostname()
	// Instance name must be unique per service type on the network.
	// Using hostname distinguishes instances; port is already in the mDNS record.
	instanceName := hostname

	txt := []string{
		"version=" + pm.version,
		"host=" + hostname,
	}

	server, err := zeroconf.Register(instanceName, mdnsServiceType, mdnsDomain, port, txt, nil)
	if err != nil {
		pm.proxyLogger.Warnf("mDNS: failed to register service: %v", err)
		return
	}

	pm.proxyLogger.Infof("mDNS: registered %s as %s on port %d", instanceName, mdnsServiceType, port)

	go func() {
		<-pm.shutdownCtx.Done()
		server.Shutdown()
		pm.proxyLogger.Debugf("mDNS: service unregistered")
	}()
}
