//go:build race

package vulnscan

// raceEnabled: the race detector slows code many times over, so wall-clock
// bounds in tests do not hold under it.
const raceEnabled = true
