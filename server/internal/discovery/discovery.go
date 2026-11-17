// Package discovery answers the TV app's UDP broadcast (PLAN.md section 6,
// "Discovery"): the client sends MINIMEDIA_DISCOVER_V1 to port 7360 and the
// server replies unicast with its name, id and URL.
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
)

const (
	Port    = 7360
	Payload = "MINIMEDIA_DISCOVER_V1"
)

// Reply is the JSON sent back to the client.
type Reply struct {
	Name     string `json:"name"`
	ServerID string `json:"serverId"`
	URL      string `json:"url"`
	Version  string `json:"version"`
}

type Server struct {
	conn     *net.UDPConn
	name     string
	serverID string
	version  string
	httpPort int
	log      *slog.Logger
	// interfaces is replaceable in tests.
	interfaces func() ([]Iface, error)
}

// Listen binds the discovery socket. addr is normally ":7360".
func Listen(addr string, name, serverID, version string, httpPort int, log *slog.Logger) (*Server, error) {
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", ua)
	if err != nil {
		return nil, fmt.Errorf("discovery: listen on UDP %s: %w", addr, err)
	}
	return &Server{conn: conn, name: name, serverID: serverID, version: version, httpPort: httpPort, log: log,
		interfaces: SystemInterfaces}, nil
}

// Addr is the bound address.
func (s *Server) Addr() *net.UDPAddr { return s.conn.LocalAddr().(*net.UDPAddr) }

// Serve answers discovery packets until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.conn.Close()
	}()
	buf := make([]byte, 512)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			// Windows reports ICMP "port unreachable" for earlier replies as a
			// read error (WSAECONNRESET); it is harmless, keep serving.
			s.log.Debug("discovery read error", "err", err)
			continue
		}
		if !bytes.Equal(bytes.TrimSpace(buf[:n]), []byte(Payload)) {
			continue
		}
		ip := s.chooseIP(from.IP)
		reply := Reply{Name: s.name, ServerID: s.serverID, Version: s.version,
			URL: fmt.Sprintf("http://%s", net.JoinHostPort(ip.String(), fmt.Sprint(s.httpPort)))}
		b, _ := json.Marshal(reply)
		if _, err := s.conn.WriteToUDP(b, from); err != nil {
			s.log.Warn("discovery reply failed", "to", from, "err", err)
			continue
		}
		s.log.Debug("discovery reply", "to", from, "url", reply.URL)
	}
}

func (s *Server) chooseIP(sender net.IP) net.IP {
	ifaces, err := s.interfaces()
	if err != nil {
		s.log.Debug("list interfaces", "err", err)
	}
	if ip := ChooseIP(sender, ifaces); ip != nil {
		return ip
	}
	if ip := routeIP(sender); ip != nil {
		return ip
	}
	return net.IPv4(127, 0, 0, 1)
}

// Iface is the part of a network interface used to pick a reply address.
type Iface struct {
	Name  string
	Up    bool
	Loop  bool
	Addrs []*net.IPNet
}

// SystemInterfaces lists the machine's interfaces.
func SystemInterfaces() ([]Iface, error) {
	list, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Iface
	for _, in := range list {
		addrs, err := in.Addrs()
		if err != nil {
			continue
		}
		f := Iface{Name: in.Name, Up: in.Flags&net.FlagUp != 0, Loop: in.Flags&net.FlagLoopback != 0}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				f.Addrs = append(f.Addrs, n)
			}
		}
		out = append(out, f)
	}
	return out, nil
}

var virtualNames = []string{
	"vethernet", "hyper-v", "wsl", "virtualbox", "vbox", "vmware", "vmnet", "docker", "tailscale",
	"zerotier", "wireguard", "openvpn", "nordlynx", "vpn", "tap-", "tap adapter", "npcap", "bluetooth",
}

// IsVirtual reports whether an adapter name looks like a VM, container or VPN adapter.
func IsVirtual(name string) bool {
	n := strings.ToLower(name)
	for _, v := range virtualNames {
		if strings.Contains(n, v) {
			return true
		}
	}
	return false
}

// ChooseIP picks the address of the interface on the sender's subnet,
// preferring physical adapters. It returns nil when no subnet matches.
func ChooseIP(sender net.IP, ifaces []Iface) net.IP {
	sender = sender.To4()
	if sender == nil {
		return nil
	}
	for _, wantVirtual := range []bool{false, true} {
		for _, in := range ifaces {
			if !in.Up || in.Loop || IsVirtual(in.Name) != wantVirtual {
				continue
			}
			for _, a := range in.Addrs {
				if a.Contains(sender) {
					return a.IP.To4()
				}
			}
		}
	}
	return nil
}

// routeIP asks the OS which local address it would use to reach the sender.
func routeIP(sender net.IP) net.IP {
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: sender, Port: Port})
	if err != nil {
		return nil
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.To4()
}
