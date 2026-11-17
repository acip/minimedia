//go:build !windows

package power

// setAwake is not implemented outside Windows yet (caffeinate/systemd-inhibit later).
var setAwake func(awake bool) error
