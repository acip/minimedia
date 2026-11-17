//go:build windows

package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/acip/minimedia/server/internal/discovery"
	"github.com/acip/minimedia/server/internal/procutil"
)

// warnPublicNetworks logs a warning for each real (not VPN/VM) network
// connection Windows treats as Public: the firewall then blocks discovery and
// the TV app on it (PLAN.md 7).
func warnPublicNetworks(ctx context.Context, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	script := `Get-NetConnectionProfile | ForEach-Object { $a = Get-NetAdapter -InterfaceIndex $_.InterfaceIndex -ErrorAction SilentlyContinue; "$($_.InterfaceAlias)|$($a.InterfaceDescription)|$($_.NetworkCategory)|$($_.Name)" }`
	out, err := procutil.Command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		log.Debug("cannot read network profiles", "err", err)
		return
	}
	for _, p := range publicNetworks(string(out)) {
		log.Warn("network is set to Public: Windows Firewall blocks the TV app and discovery on it. "+
			"In Settings → Network & internet → "+p.alias+", choose Private network",
			"adapter", p.alias, "network", p.name)
	}
}

type netProfile struct{ alias, description, category, name string }

// publicNetworks parses "alias|description|category|name" lines and keeps
// Public profiles of real adapters.
func publicNetworks(out string) []netProfile {
	var res []netProfile
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) < 4 {
			continue
		}
		p := netProfile{f[0], f[1], f[2], f[3]}
		if strings.EqualFold(p.category, "Public") && !discovery.IsVirtual(p.alias) && !discovery.IsVirtual(p.description) {
			res = append(res, p)
		}
	}
	return res
}
