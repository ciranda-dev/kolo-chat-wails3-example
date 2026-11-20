//go:build race

package main

// floodEntries is smaller under the race detector, which makes writing the flood slow. Past maxRefused it proves the
// same bound; the full 16 MiB runs without -race.
const floodEntries = 2 * maxRefused
