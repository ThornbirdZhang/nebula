package nebula

import (
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/slackhq/nebula/cert"
)

type Control struct {
	f *Interface
}

type ControlHostInfo struct {
	VpnIP          net.IP                  `json:"vpnIp"`
	LocalIndex     uint32                  `json:"localIndex"`
	RemoteIndex    uint32                  `json:"remoteIndex"`
	RemoteAddrs    []*udpAddr              `json:"remoteAddrs"`
	CachedPackets  int                     `json:"cachedPackets"`
	Cert           *cert.NebulaCertificate `json:"cert"`
	MessageCounter *uint64                 `json:"messageCounter"`
	CurrentRemote  *udpAddr                `json:"currentRemote"`
}

// Start actually runs nebula, this is a nonblocking call. To block use Control.ShutdownBlock()
func (c *Control) Start() {
	c.f.run()
}

// Stop signals nebula to shutdown, returns after the shutdown is complete
func (c *Control) Stop() {
	//TODO: stop tun and udp routines, the lock on hostMap effectively does that though
	//TODO: this is probably better as a function in ConnectionManager or HostMap directly
	c.f.hostMap.Lock()
	for _, h := range c.f.hostMap.Hosts {
		if h.ConnectionState.ready {
			c.f.send(closeTunnel, 0, h.ConnectionState, h, h.remote, []byte{}, make([]byte, 12, 12), make([]byte, mtu))
			l.WithField("vpnIp", IntIp(h.hostId)).WithField("udpAddr", h.remote).
				Debug("Sending close tunnel message")
		}
	}
	c.f.hostMap.Unlock()
}

// ShutdownBlock will listen for and block on term and interrupt signals, calling Control.Stop() once signalled
func (c *Control) ShutdownBlock() {
	sigChan := make(chan os.Signal)
	signal.Notify(sigChan, syscall.SIGTERM)
	signal.Notify(sigChan, syscall.SIGINT)

	rawSig := <-sigChan
	sig := rawSig.String()
	l.WithField("signal", sig).Info("Caught signal, shutting down")

	c.Stop()
	//TODO: move goodbye to cmd
	l.WithField("signal", sig).Info("Goodbye")
}

// RebindUDPServer asks the UDP listener to rebind it's listener. Mainly used on mobile clients when interfaces change
func (c *Control) RebindUDPServer() {
	c.f.outside.Rebind()
}

// ListHostmap returns details about the actual or pending (handshaking) hostmap
func (c *Control) ListHostmap(pendingMap bool) []ControlHostInfo {
	var hm *HostMap
	if pendingMap {
		hm = c.f.handshakeManager.pendingHostMap
	} else {
		hm = c.f.hostMap
	}

	hm.RLock()
	hosts := make([]ControlHostInfo, len(hm.Hosts))
	i := 0
	for _, v := range hm.Hosts {
		hosts[i] = c.copyHostInfo(v)
		i++
	}
	hm.RUnlock()

	return hosts
}

// GetHostInfoByVpnIp returns a single tunnels hostInfo, or null if not found
func (c *Control) GetHostInfoByVpnIp(vpnIp uint32, pending bool) *ControlHostInfo {
	var hm *HostMap
	if pending {
		hm = c.f.handshakeManager.pendingHostMap
	} else {
		hm = c.f.hostMap
	}

	h, err := hm.QueryVpnIP(vpnIp)
	if err != nil {
		return nil
	}

	ch := c.copyHostInfo(h)
	return &ch
}

// SetRemoteForTunnel forces a tunnel to use a specific remote
func (c *Control) SetRemoteForTunnel(vpnIp uint32, addr udpAddr) *ControlHostInfo {
	hostInfo, err := c.f.hostMap.QueryVpnIP(vpnIp)
	if err != nil {
		return nil
	}

	hostInfo.SetRemote(addr)

	ch := c.copyHostInfo(hostInfo)
	return &ch
}

// CloseTunnel closes a fully established tunnel. If localOnly is false it will notify the remote end as well.
func (c *Control) CloseTunnel(vpnIp uint32, localOnly bool) bool {
	hostInfo, err := c.f.hostMap.QueryVpnIP(vpnIp)
	if err != nil {
		return false
	}

	if localOnly {
		c.f.send(
			closeTunnel,
			0,
			hostInfo.ConnectionState,
			hostInfo,
			hostInfo.remote,
			[]byte{},
			make([]byte, 12, 12),
			make([]byte, mtu),
		)
	}

	c.f.closeTunnel(hostInfo)
	return true
}

func (c *Control) copyHostInfo(h *HostInfo) ControlHostInfo {
	return ControlHostInfo{
		VpnIP:          int2ip(h.hostId),
		LocalIndex:     h.localIndexId,
		RemoteIndex:    h.remoteIndexId,
		RemoteAddrs:    h.RemoteUDPAddrs(),
		CachedPackets:  len(h.packetStore),
		Cert:           h.GetCert(),
		CurrentRemote:  h.remote,
		MessageCounter: h.ConnectionState.messageCounter,
	}
}