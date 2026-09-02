package client

import (
	"testing"
)

func TestPeerQueueSizeFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want int
	}{
		{name: "unset uses default", env: "", want: defaultPeerQueueSize},
		{name: "valid value", env: "4096", want: 4096},
		{name: "not a number uses default", env: "big", want: defaultPeerQueueSize},
		{name: "zero uses default", env: "0", want: defaultPeerQueueSize},
		{name: "negative uses default", env: "-5", want: defaultPeerQueueSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envPeerQueueSize, tt.env)
			if got := peerQueueSizeFromEnv(); got != tt.want {
				t.Fatalf("peerQueueSizeFromEnv() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNewClientUsesEnvQueueSize(t *testing.T) {
	t.Setenv(envPeerQueueSize, "7")
	c := NewClient("rel://127.0.0.1:1", hmacTokenStore, "alice", 1280)
	if c.peerQueueSize != 7 {
		t.Fatalf("peerQueueSize = %d, want 7", c.peerQueueSize)
	}
	cc := newConnContainer(c.log, c, c.hashedID, nil)
	if cap(cc.messages) != 7 {
		t.Fatalf("cap(messages) = %d, want 7", cap(cc.messages))
	}
}
