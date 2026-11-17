// Command discover is a small test client for MiniMedia's UDP discovery. It
// broadcasts MINIMEDIA_DISCOVER_V1 to 255.255.255.255:7360 and every subnet
// broadcast address (as the TV app does), then prints each reply.
//
//	go run ./cmd/discover              # broadcast
//	go run ./cmd/discover -to 127.0.0.1 # unicast to one host
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

const (
	port    = 7360
	payload = "MINIMEDIA_DISCOVER_V1"
)

func main() {
	to := flag.String("to", "", "send to this host only instead of broadcasting")
	wait := flag.Duration("wait", 2*time.Second, "how long to wait for replies")
	flag.Parse()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close()

	var targets []net.IP
	if *to != "" {
		ips, err := net.LookupIP(*to)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		targets = append(targets, ips[0])
	} else {
		targets = append(targets, net.IPv4bcast)
		targets = append(targets, subnetBroadcasts()...)
	}
	// Send three times over the wait period, like the app's retry.
	go func() {
		for i := 0; i < 3; i++ {
			for _, ip := range targets {
				if _, err := conn.WriteToUDP([]byte(payload), &net.UDPAddr{IP: ip, Port: port}); err != nil {
					fmt.Fprintf(os.Stderr, "send to %s: %v\n", ip, err)
				}
			}
			time.Sleep(*wait / 3)
		}
	}()

	seen := map[string]bool{}
	deadline := time.Now().Add(*wait)
	buf := make([]byte, 2048)
	for {
		conn.SetReadDeadline(deadline)
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			continue // e.g. WSAECONNRESET on Windows
		}
		msg := string(buf[:n])
		if seen[msg] {
			continue
		}
		seen[msg] = true
		fmt.Printf("reply from %s: %s\n", from, msg)
	}
	if len(seen) == 0 {
		fmt.Println("no replies")
		os.Exit(1)
	}
}

func subnetBroadcasts() []net.IP {
	var out []net.IP
	ifaces, _ := net.Interfaces()
	for _, in := range ifaces {
		if in.Flags&net.FlagUp == 0 || in.Flags&net.FlagLoopback != 0 || in.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ip := n.IP.To4()
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^n.Mask[len(n.Mask)-4+i]
			}
			out = append(out, b)
		}
	}
	return out
}
