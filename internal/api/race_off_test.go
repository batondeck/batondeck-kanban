//go:build !race

package api

// raceDetector is false in a build without -race (race_on_test.go says why it matters).
const raceDetector = false
