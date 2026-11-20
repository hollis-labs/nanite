//go:build race

package elicitation

// raceEnabled is true when the tests run with the race detector, which slows
// and grows the child processes several times over.
const raceEnabled = true
