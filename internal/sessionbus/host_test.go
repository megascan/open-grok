package sessionbus_test

import (
	"testing"
	"time"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/sessionbus"
)

func TestBusRoundTrip(t *testing.T) {
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	t.Cleanup(sessionbus.Stop)

	got := make(chan sessionbus.Delivered, 1)
	host, err := sessionbus.Start(func(d sessionbus.Delivered) string {
		got <- d
		return "Accepted"
	})
	if err != nil {
		t.Fatal(err)
	}
	host.Register(sessionbus.SessionInfo{
		SessionID:   "sess-a",
		CWD:         "/tmp/proj",
		ProjectName: "proj",
		ModelID:     "grok-4",
		Status:      "idle",
	})
	time.Sleep(20 * time.Millisecond)
	list := sessionbus.LiveSessions()
	if len(list) != 1 || list[0].SessionID != "sess-a" {
		t.Fatalf("live=%v", list)
	}
	status, err := sessionbus.Send("sess-a", "sess-b", "other", "hello peer")
	if err != nil || status != "Accepted" {
		t.Fatalf("send %s %v", status, err)
	}
	select {
	case d := <-got:
		if d.Body != "hello peer" || d.TargetSession != "sess-a" {
			t.Fatalf("%+v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inbox timeout")
	}
}

func TestUnknownSession(t *testing.T) {
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	t.Cleanup(sessionbus.Stop)
	_, err := sessionbus.Start(nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err := sessionbus.Send("missing", "me", "p", "x")
	if status != "UnknownSession" || err == nil {
		t.Fatalf("status=%s err=%v", status, err)
	}
}
