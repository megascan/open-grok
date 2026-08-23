package acp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/acp"
	"github.com/megascan/open-grok/internal/config"
)

func TestStdioInitializeAndSessionNew(t *testing.T) {
	t.Setenv("OPENGROK_HOME", t.TempDir())
	config.ResetHomeForTest()
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"."}}` + "\n")
	var out bytes.Buffer
	if err := acp.ServeStdio(context.Background(), acp.Options{CWD: t.TempDir(), In: in, Out: &out, YOLO: true}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("got %q", out.String())
	}
	var init map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &init); err != nil {
		t.Fatal(err)
	}
	res := init["result"].(map[string]any)
	if res["protocolVersion"].(float64) != 1 {
		t.Fatalf("protocol %v", res["protocolVersion"])
	}
	var newsess map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &newsess); err != nil {
		t.Fatal(err)
	}
	sid := newsess["result"].(map[string]any)["sessionId"].(string)
	if sid == "" {
		t.Fatal("missing sessionId")
	}
}
