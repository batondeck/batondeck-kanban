//go:build !race

package cli

// raceDetector is false in a build without -race (race_on_test.go says why it matters).
const raceDetector = false
