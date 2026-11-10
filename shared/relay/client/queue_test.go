package client

import (
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

// captureStdLog records entries of the standard logger until the test ends.
func captureStdLog(t *testing.T) *test.Hook {
	t.Helper()
	logger := log.StandardLogger()
	saved := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(saved) })
	return test.NewLocal(logger)
}

func TestPeerQueueSizeFromEnv(t *testing.T) {
	const (
		noWarn   = ""
		invalid  = "invalid"
		aboveMax = "is above the maximum"
	)
	tests := []struct {
		name string
		env  string
		want int
		warn string
	}{
		{name: "unset uses default", env: "", want: defaultPeerQueueSize, warn: noWarn},
		{name: "valid value", env: "4096", want: 4096, warn: noWarn},
		{name: "surrounding whitespace is trimmed", env: " \t4096\n ", want: 4096, warn: noWarn},
		{name: "whitespace only uses default", env: "  ", want: defaultPeerQueueSize, warn: noWarn},
		{name: "not a number uses default", env: "big", want: defaultPeerQueueSize, warn: invalid},
		{name: "number with suffix uses default", env: "4096k", want: defaultPeerQueueSize, warn: invalid},
		{name: "zero uses default", env: "0", want: defaultPeerQueueSize, warn: invalid},
		{name: "negative uses default", env: "-5", want: defaultPeerQueueSize, warn: invalid},
		{name: "negative beyond int range uses default", env: "-99999999999999999999", want: defaultPeerQueueSize, warn: invalid},
		{name: "max is kept", env: "65536", want: maxPeerQueueSize, warn: noWarn},
		{name: "above max is capped", env: "65537", want: maxPeerQueueSize, warn: aboveMax},
		{name: "beyond int range is capped", env: "99999999999999999999", want: maxPeerQueueSize, warn: aboveMax},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := captureStdLog(t)
			t.Setenv(envPeerQueueSize, tt.env)
			if got := peerQueueSizeFromEnv(); got != tt.want {
				t.Fatalf("peerQueueSizeFromEnv() = %d, want %d", got, tt.want)
			}
			var warnings []string
			for _, e := range hook.AllEntries() {
				if e.Level == log.WarnLevel && strings.Contains(e.Message, envPeerQueueSize) {
					warnings = append(warnings, e.Message)
				}
			}
			switch {
			case tt.warn == noWarn && len(warnings) != 0:
				t.Fatalf("warnings = %q, want none", warnings)
			case tt.warn != noWarn && (len(warnings) != 1 || !strings.Contains(warnings[0], tt.warn)):
				t.Fatalf("warnings = %q, want one containing %q", warnings, tt.warn)
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

func TestWriteMsgQueueFullDropsAndCounts(t *testing.T) {
	t.Setenv(envPeerQueueSize, "3")
	c := NewClient("rel://127.0.0.1:1", hmacTokenStore, "alice", 1280)
	cc := newConnContainer(c.log, c, c.hashedID, nil)

	newMsg := func() Msg {
		bufPtr := c.bufPool.Get().(*[]byte)
		return Msg{bufPool: c.bufPool, bufPtr: bufPtr, Payload: (*bufPtr)[:1]}
	}

	for i := 0; i < 5; i++ {
		cc.writeMsg(newMsg())
	}

	if got := len(cc.messages); got != 3 {
		t.Fatalf("queued = %d, want 3", got)
	}
	if got := cc.dropped; got != 2 {
		t.Fatalf("dropped = %d, want 2", got)
	}
}
