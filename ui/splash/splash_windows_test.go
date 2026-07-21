//go:build windows

package splash

import (
	"testing"
	"time"
)

func newTestSplash() *winSplash {
	return newPlatform(Config{AppName: "test"})
}

func withFastSendGuaranteedTimeout(t *testing.T) {
	t.Helper()
	orig := sendGuaranteedTimeout
	sendGuaranteedTimeout = 20 * time.Millisecond
	t.Cleanup(func() { sendGuaranteedTimeout = orig })
}

func TestSendGuaranteedNoOpWhenNotRunning(t *testing.T) {
	s := newTestSplash()
	// running is false (zero value) — must return immediately without
	// touching s.cmds, since nothing is consuming it.
	done := make(chan struct{})
	go func() {
		s.sendGuaranteed(splashCmd{kind: cmdHide})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sendGuaranteed blocked despite running=false")
	}

	select {
	case <-s.cmds:
		t.Fatal("expected no command to be enqueued while not running")
	default:
	}
}

func TestSendGuaranteedDeliversWhenConsumerIsReading(t *testing.T) {
	withFastSendGuaranteedTimeout(t)
	s := newTestSplash()
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	received := make(chan splashCmd, 1)
	go func() {
		received <- <-s.cmds
	}()

	s.sendGuaranteed(splashCmd{kind: cmdHide})

	select {
	case cmd := <-received:
		if cmd.kind != cmdHide {
			t.Fatalf("expected cmdHide, got %+v", cmd)
		}
	case <-time.After(time.Second):
		t.Fatal("command was never delivered to the consumer")
	}
}

func TestSendGuaranteedGivesUpAfterTimeoutInsteadOfBlockingForever(t *testing.T) {
	withFastSendGuaranteedTimeout(t)
	s := newTestSplash()
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	// Fill the buffer and leave nothing reading from s.cmds, simulating the
	// exact failure mode this fix targets — the "always drop" version of
	// this bug would return instantly and silently; this must instead
	// return only after sendGuaranteedTimeout elapses.
	for i := 0; i < cap(s.cmds); i++ {
		s.cmds <- splashCmd{kind: cmdUpdate}
	}

	start := time.Now()
	done := make(chan struct{})
	go func() {
		s.sendGuaranteed(splashCmd{kind: cmdHide})
		close(done)
	}()

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed < sendGuaranteedTimeout {
			t.Fatalf("returned after %v, before the %v timeout elapsed", elapsed, sendGuaranteedTimeout)
		}
	case <-time.After(time.Second):
		t.Fatal("sendGuaranteed blocked well past its timeout — would hang forever in production")
	}
}
