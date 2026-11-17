//go:build windows

package main

import "testing"

func TestPublicNetworks(t *testing.T) {
	out := "Wi-Fi 2|Intel(R) Wi-Fi 6 AX201 160MHz|Private|Home\r\n" +
		"wg-home|WireGuard Tunnel|Public|wg-home\r\n" +
		"Ethernet|Realtek PCIe GbE Family Controller|Public|Network 3\r\n" +
		"vEthernet (WSL)|Hyper-V Virtual Ethernet Adapter|Public|Unidentified network\r\n"
	got := publicNetworks(out)
	if len(got) != 1 || got[0].alias != "Ethernet" {
		t.Errorf("got %+v", got)
	}
}
