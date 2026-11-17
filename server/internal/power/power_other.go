//go:build !windows && !darwin && !linux

package power

// setAwake is not available on this OS: the computer's own sleep settings apply.
var setAwake func(awake bool) error
