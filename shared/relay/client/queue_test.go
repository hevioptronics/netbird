package client

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"

	"github.com/netbirdio/netbird/shared/relay/messages"
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

// fakeClock is a manually advanced clock for connContainer.now.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

var testRemotePeer = messages.HashID("bob")

// newTestContainer returns a container for testRemotePeer with a queue of size, a fake
// clock and a hook on the container logger.
func newTestContainer(t *testing.T, size int) (*connContainer, *fakeClock, *test.Hook, func() Msg) {
	t.Helper()
	c := NewClient("rel://127.0.0.1:1", hmacTokenStore, "alice", 1280)
	c.peerQueueSize = size
	logger, hook := test.NewNullLogger()
	cc := newConnContainer(log.NewEntry(logger), c, testRemotePeer, nil)
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	cc.now = clock.now
	newMsg := func() Msg {
		bufPtr := c.bufPool.Get().(*[]byte)
		return Msg{bufPool: c.bufPool, bufPtr: bufPtr, Payload: (*bufPtr)[:1]}
	}
	return cc, clock, hook, newMsg
}

var droppedRe = regexp.MustCompile(`dropped (\d+) messages`)

// dropWarnings returns each queue-full warning in entries as "dropping" or "dropped N".
func dropWarnings(t *testing.T, entries []*log.Entry) []string {
	t.Helper()
	var got []string
	for _, e := range entries {
		if e.Level != log.WarnLevel || !strings.Contains(e.Message, "peer message queue full") {
			t.Fatalf("unexpected log entry: %s %q", e.Level, e.Message)
		}
		if peer := e.Data["peer_id"]; peer != testRemotePeer.String() {
			t.Fatalf("peer_id = %v, want %s", peer, testRemotePeer.String())
		}
		switch m := droppedRe.FindStringSubmatch(e.Message); {
		case m != nil:
			got = append(got, "dropped "+m[1])
		case strings.Contains(e.Message, "dropping messages"):
			got = append(got, "dropping")
		default:
			t.Fatalf("unexpected warning: %q", e.Message)
		}
	}
	return got
}

func TestWriteMsgDropWarning(t *testing.T) {
	type step struct {
		advance time.Duration // clock advance before the drops
		drops   int           // messages written to the full queue
		close   bool          // close the container after the drops
		want    []string      // warnings logged by this step
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{name: "burst within one interval", steps: []step{
			{drops: 50, want: []string{"dropping"}},
			{advance: dropLogInterval - time.Millisecond, drops: 5},
			{close: true, want: []string{"dropped 55"}},
		}},
		{name: "burst across an interval", steps: []step{
			{drops: 50, want: []string{"dropping"}},
			{advance: dropLogInterval, drops: 1, want: []string{"dropped 51"}},
			{advance: time.Second, drops: 4},
			{close: true, want: []string{"dropped 4"}},
		}},
		{name: "new burst after a quiet interval", steps: []step{
			{drops: 1, want: []string{"dropping"}},
			{advance: dropLogInterval, drops: 1, want: []string{"dropped 2"}},
			{advance: dropLogInterval, drops: 1, want: []string{"dropping"}},
			{close: true, want: []string{"dropped 1"}},
		}},
		{name: "close with every drop reported", steps: []step{
			{drops: 2, want: []string{"dropping"}},
			{advance: dropLogInterval, drops: 1, want: []string{"dropped 3"}},
			{close: true},
		}},
		{name: "close without drops", steps: []step{
			{close: true},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const size = 3
			cc, clock, hook, newMsg := newTestContainer(t, size)
			for i := 0; i < size; i++ {
				cc.writeMsg(newMsg())
			}
			seen := len(hook.AllEntries())
			if seen != 0 {
				t.Fatalf("warnings before the queue was full: %q", dropWarnings(t, hook.AllEntries()))
			}
			var reported uint64
			for i, s := range tt.steps {
				clock.t = clock.t.Add(s.advance)
				for j := 0; j < s.drops; j++ {
					cc.writeMsg(newMsg())
				}
				if s.close {
					cc.close(nil)
				}
				entries := hook.AllEntries()
				got := dropWarnings(t, entries[seen:])
				seen = len(entries)
				if !slices.Equal(got, s.want) {
					t.Fatalf("step %d: warnings = %q, want %q", i, got, s.want)
				}
				for _, w := range got {
					var n uint64
					if _, err := fmt.Sscanf(w, "dropped %d", &n); err == nil {
						reported += n
					}
				}
			}
			if reported != cc.dropped {
				t.Fatalf("reported %d drops, dropped %d", reported, cc.dropped)
			}
		})
	}
}

func TestCloseFreesQueueAndIgnoresLateWrites(t *testing.T) {
	const size = 3
	cc, _, hook, newMsg := newTestContainer(t, size)
	for i := 0; i < size+2; i++ {
		cc.writeMsg(newMsg())
	}
	if got := len(cc.messages); got != size {
		t.Fatalf("queued = %d, want %d", got, size)
	}
	if cc.dropped != 2 {
		t.Fatalf("dropped = %d, want 2", cc.dropped)
	}

	cc.close(nil)
	// close frees each message it drains, so an empty closed queue means every buffer was freed.
	if got := len(cc.messages); got != 0 {
		t.Fatalf("queued after close = %d, want 0", got)
	}
	if _, ok := <-cc.messages; ok {
		t.Fatal("queue is still open after close")
	}

	logged := len(hook.AllEntries())
	// A send on the closed queue panics. close also cancels ctx, so select would pick
	// that send only half of the time without the closed check: write many times.
	for i := 0; i < 100; i++ {
		cc.writeMsg(newMsg())
	}
	cc.close(nil)
	if cc.dropped != 2 {
		t.Fatalf("dropped after a late write = %d, want 2", cc.dropped)
	}
	if got := hook.AllEntries()[logged:]; len(got) != 0 {
		t.Fatalf("late write logged %q", dropWarnings(t, got))
	}
}
