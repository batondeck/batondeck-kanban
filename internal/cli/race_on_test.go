//go:build race

package cli

// raceDetector is true in a -race build (race_off_test.go is the other half). The board stream's
// 200 ms bound from a write's return to its frame is asserted only without it, by make
// stream-latency: the detector slows the frame's whole path several-fold, and make test runs it
// beside other packages on the same CPUs, where correct frames came 230-530 ms late.
const raceDetector = true
