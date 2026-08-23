package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/sessionbus"
	"github.com/megascan/open-grok/internal/tools"
)

func TestTeamMailbox(t *testing.T) {
	box := tools.NewTeamBox()
	box.Register("child-1", "worker")
	res := tools.ListAgents{Box: box}.Execute(context.Background(), tools.Context{}, nil)
	if res.IsError || !contains(res.Content, "child-1") {
		t.Fatalf("%s", res.Content)
	}
	sendArgs, _ := json.Marshal(map[string]string{"agent_id": "child-1", "message": "steer"})
	res = tools.SendMessage{Box: box}.Execute(context.Background(), tools.Context{}, sendArgs)
	if res.IsError {
		t.Fatal(res.Content)
	}
	box.Finish("child-1", "done", false)
	waitArgs, _ := json.Marshal(map[string]string{"agent_id": "child-1"})
	res = tools.WaitAgent{Box: box}.Execute(context.Background(), tools.Context{}, waitArgs)
	if res.IsError || !contains(res.Content, "idle") {
		t.Fatalf("wait %s", res.Content)
	}
}

func TestSessionBusTools(t *testing.T) {
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	t.Cleanup(sessionbus.Stop)
	host, err := sessionbus.Start(func(d sessionbus.Delivered) string { return "Accepted" })
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	host.Register(sessionbus.SessionInfo{SessionID: "abc", CWD: cwd, ProjectName: "p", ModelID: "m", Status: "idle"})
	dir := filepath.Join(config.SessionsCWDDirIn(config.Home(), cwd), "abc")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "session.json"), []byte(`{"title":"t","messages":[{"role":"user","content":"hi"}]}`), 0o600)

	res := tools.ListSessions{}.Execute(context.Background(), tools.Context{}, nil)
	if res.IsError || !contains(res.Content, "abc") {
		t.Fatalf("list %s", res.Content)
	}
	args, _ := json.Marshal(map[string]string{"session_id": "abc"})
	res = tools.ReadSession{}.Execute(context.Background(), tools.Context{}, args)
	if res.IsError || !contains(res.Content, "hi") {
		t.Fatalf("read %s", res.Content)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()))
}

func TestUnboundTeamToolsDoNotPanic(t *testing.T) {
	res := tools.ListAgents{}.Execute(context.Background(), tools.Context{}, nil)
	if !res.IsError || !contains(res.Content, "not bound") {
		t.Fatalf("list %s", res.Content)
	}
	res = tools.WaitAgent{}.Execute(context.Background(), tools.Context{}, json.RawMessage(`{"agent_id":"x"}`))
	if !res.IsError {
		t.Fatalf("wait %s", res.Content)
	}
}

func TestDirectCollabPackNames(t *testing.T) {
	names := map[string]bool{}
	for _, ttool := range tools.DefaultPack() {
		names[ttool.Name()] = true
	}
	for _, n := range []string{"list_sessions", "read_session", "message_session", "list_agents", "task"} {
		if !names[n] {
			t.Errorf("missing %s", n)
		}
	}
}
