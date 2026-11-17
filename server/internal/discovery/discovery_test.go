package discovery

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func ipnet(s string) *net.IPNet {
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	n.IP = ip
	return n
}

func TestChooseIP(t *testing.T) {
	ifaces := []Iface{
		{Name: "Loopback Pseudo-Interface 1", Up: true, Loop: true, Addrs: []*net.IPNet{ipnet("127.0.0.1/8")}},
		{Name: "vEthernet (WSL)", Up: true, Addrs: []*net.IPNet{ipnet("172.20.0.1/20")}},
		{Name: "vEthernet (Default Switch)", Up: true, Addrs: []*net.IPNet{ipnet("192.168.1.200/24")}}, // overlaps the LAN
		{Name: "Ethernet", Up: true, Addrs: []*net.IPNet{ipnet("192.168.1.20/24")}},
		{Name: "Wi-Fi", Up: false, Addrs: []*net.IPNet{ipnet("10.0.0.5/24")}},
		{Name: "Tailscale", Up: true, Addrs: []*net.IPNet{ipnet("100.64.1.2/10")}},
	}
	cases := []struct {
		sender, want string
	}{
		{"192.168.1.42", "192.168.1.20"}, // physical adapter wins over an overlapping virtual one
		{"172.20.5.5", "172.20.0.1"},     // sender on a virtual subnet gets the virtual address
		{"10.0.0.9", ""},                 // interface down
		{"8.8.8.8", ""},                  // no subnet match
		{"127.0.0.1", ""},                // loopback is never chosen by subnet
	}
	for _, c := range cases {
		got := ChooseIP(net.ParseIP(c.sender), ifaces)
		if (got == nil && c.want != "") || (got != nil && got.String() != c.want) {
			t.Errorf("ChooseIP(%s) = %v, want %q", c.sender, got, c.want)
		}
	}
	for name, want := range map[string]bool{"Ethernet": false, "Wi-Fi": false, "vEthernet (WSL)": true,
		"OpenVPN TAP-Windows6": true, "VirtualBox Host-Only Network": true, "Ethernet 2": false} {
		if IsVirtual(name) != want {
			t.Errorf("IsVirtual(%q) = %v", name, !want)
		}
	}
}

func TestServeReplies(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := Listen("127.0.0.1:0", "Test Server", "abc123", "1.0", 8097, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx)

	c, err := net.DialUDP("udp4", nil, s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Junk must be ignored, the real payload answered.
	c.Write([]byte("HELLO"))
	c.Write([]byte(Payload + "\n"))
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1024)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var r Reply
	if err := json.Unmarshal(buf[:n], &r); err != nil {
		t.Fatalf("bad reply %q: %v", buf[:n], err)
	}
	if r.Name != "Test Server" || r.ServerID != "abc123" || r.Version != "1.0" || r.URL != "http://127.0.0.1:8097" {
		t.Errorf("reply = %+v", r)
	}
	// No second reply (the junk packet was dropped).
	c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := c.Read(buf); err == nil {
		t.Error("got a reply to a junk packet")
	}
}
