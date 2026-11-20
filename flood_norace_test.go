//go:build !race

package main

// floodEntries is the most entries a 16 MiB pin quota holds: kolo-log pads each to 256 bytes at least.
const floodEntries = defaultPinQuota / 256
