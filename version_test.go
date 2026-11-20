package main

import (
	"encoding/json"
	"testing"
)

// Start tells the window which release runs: "dev" for a build that is not a release, the injected tag otherwise.
func TestStartReportsTheVersion(t *testing.T) {
	if version != "dev" {
		t.Fatalf("a build without -ldflags has version %q, want dev", version)
	}
	old := version
	t.Cleanup(func() { version = old })
	for _, want := range []string{"dev", "v0.1.15"} {
		version = want
		a := NewConversations(Config{DataDir: dataDir(t)}, (&convEvents{}).emit)
		info, err := a.Start()
		if err != nil {
			t.Fatal(err)
		}
		a.ServiceShutdown()
		b, _ := json.Marshal(info)
		var got struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(b, &got); err != nil || got.Version != want || info.Version != want {
			t.Fatalf("version %q in %s, want %q, %v", info.Version, b, want, err)
		}
	}
}
