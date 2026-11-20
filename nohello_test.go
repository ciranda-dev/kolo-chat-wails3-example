package main

import (
	"testing"
	"time"

	koloid "github.com/ciranda-dev/kolo-id"
)

// Someone who found the conversation's topic and never sends a hello holds a connection and a peer slot of the swarm
// for no longer than sendTimeout: the topic alone does not keep a connection.
func TestStrangerWithNoHelloIsDisconnected(t *testing.T) {
	n := newNetwork(t)
	a, _ := start(t, n.config(dataDir(t)))
	carol, _ := koloid.Generate()
	conn := strangerConn(t, n, a, carol)
	select {
	case <-conn.Context().Done():
	case <-time.After(sendTimeout + 20*time.Second):
		t.Fatal("a connection that sent no hello stays open")
	}
}
