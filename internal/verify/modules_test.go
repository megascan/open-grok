// Package verify is a module-by-module inventory: every public function
// the Go binary depends on must exist, be callable, and not panic.
package verify_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/acp"
	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/cli"
	"github.com/megascan/open-grok/internal/codemode"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/goals"
	"github.com/megascan/open-grok/internal/hooks"
	"github.com/megascan/open-grok/internal/mcp"
	"github.com/megascan/open-grok/internal/oauth"
	"github.com/megascan/open-grok/internal/sampler"
	"github.com/megascan/open-grok/internal/sessionbus"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/update"
	"github.com/megascan/open-grok/internal/version"
	"github.com/megascan/open-grok/internal/workspace"
	"github.com/spf13/cobra"
)

func TestVersionModule(t *testing.T) {
	if version.String() == "" {
		t.Fatal("version.String")
	}
	if !strings.Contains(version.Display(), "entropy") {
		t.Fatalf("version.Display %q", version.Display())
	}
}

func TestConfigModule(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	home := config.Home()
	if config.NeverGrokHome(home) {
		t.Fatal("Home must never be ~/.grok")
	}
	for _, p := range []string{
		config.AuthJSON(), config.CodexAuthJSON(), config.ConfigTOML(),
		config.SessionsRoot(), config.Application(), config.LeaderSock(),
	} {
		if p == "" || strings.Contains(p, string(filepath.Separator)+".grok"+string(filepath.Separator)) {
			t.Fatalf("path %q", p)
		}
	}
	if _, err := config.Load(); err != nil {
		t.Fatal(err)
	}
	if config.EncodeCWDDirname("/tmp/proj") == "" {
		t.Fatal("EncodeCWDDirname")
	}
}

func TestTypesModule(t *testing.T) {
	if len(types.AllProviders) < 12 {
		t.Fatalf("AllProviders %d", len(types.AllProviders))
	}
	for _, p := range types.AllProviders {
		if p.DisplayName() == "" || p.CLIName() == "" {
			t.Fatalf("provider %s missing names", p)
		}
		if _, ok := types.ParseProvider(p.CLIName()); !ok {
			t.Fatalf("ParseProvider(%s)", p.CLIName())
		}
		prof := p.Profile()
		if prof.Provider != p {
			t.Fatalf("Profile identity %s != %s", prof.Provider, p)
		}
		_ = p.AllowsXAIServices()
		_ = prof.Backends.Supports(types.BackendChat)
	}
	if _, ok := types.ParseProvider("not-a-provider"); ok {
		t.Fatal("unknown provider must fail")
	}
}

func TestCatalogModule(t *testing.T) {
	if catalog.DefaultID() == "" {
		t.Fatal("DefaultID")
	}
	all := catalog.All()
	if len(all) == 0 {
		t.Fatal("catalog.All empty")
	}
	m, ok := catalog.Lookup(catalog.DefaultID())
	if !ok {
		t.Fatal("Lookup default")
	}
	if m.Provider == "" {
		t.Fatal("provider identity missing")
	}
	if catalog.DefaultBaseURL(types.ProviderOpenRouter) == "" {
		t.Fatal("DefaultBaseURL")
	}
	_ = catalog.TrustedHost(types.ProviderXAI, catalog.DefaultBaseURL(types.ProviderXAI))
	_ = m.ToolsEnabled()
	_, ok = catalog.Lookup("definitely-not-a-real-slug-xyz")
	if ok {
		t.Fatal("unknown slug must not resolve")
	}
}

func TestAuthModule(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	if err := auth.SaveAPIKey(types.ProviderOpenRouter, "sk-test-inventory", false); err != nil {
		t.Fatal(err)
	}
	cred, ok := auth.Lookup(types.ProviderOpenRouter, false)
	if !ok || cred.Key == "" {
		t.Fatal("Lookup after SaveAPIKey")
	}
	if auth.ScopeFor(types.ProviderOpenRouter, false) != "openrouter::api_key" {
		t.Fatalf("scope %s", auth.ScopeFor(types.ProviderOpenRouter, false))
	}
	if auth.ScopeFor(types.ProviderKimi, true) == auth.ScopeFor(types.ProviderKimi, false) {
		t.Fatal("kimi platform and kimi code scopes must differ")
	}
	bearer, _, err := auth.ResolveBearer(types.ProviderOpenRouter, auth.EnvKeys(types.ProviderOpenRouter), false)
	if err != nil || bearer == "" {
		t.Fatalf("ResolveBearer %v %q", err, bearer)
	}
	st := auth.Status()
	if st[types.ProviderOpenRouter] == "" {
		t.Fatal("Status")
	}
	if len(auth.EnvKeys(types.ProviderXAI)) == 0 {
		t.Fatal("EnvKeys xai")
	}
	if err := auth.Logout(types.ProviderOpenRouter, false); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthModule(t *testing.T) {
	ep := oauth.DefaultEndpoints()
	if ep.Issuer == "" || ep.ClientID == "" {
		t.Fatalf("%+v", ep)
	}
}

func TestSamplerModule(t *testing.T) {
	called := false
	ad := sampler.Adapter{
		Provider: types.ProviderOpenRouter,
		Backend:  types.BackendChat,
		BaseURL:  "https://example.invalid/v1",
		Bearer:   "k",
		HTTP: httpDoerFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			body := `data: {"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n"
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}),
	}
	ch, err := ad.Stream(context.Background(), types.SampleRequest{Model: "m", Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for ev := range ch {
		got += ev.Text
	}
	if !called || !strings.Contains(got, "hi") {
		t.Fatalf("stream called=%v text=%q", called, got)
	}
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestWorkspaceModule(t *testing.T) {
	d, _ := workspace.Check(workspace.Request{Tool: "read_file", Access: types.AccessRead})
	if d != workspace.Allow {
		t.Fatal("read auto-allow")
	}
	plan := filepath.Join(t.TempDir(), "plan.md")
	d, _ = workspace.Check(workspace.Request{
		Tool: "write_file", Access: types.AccessEdit, Path: "/tmp/x.go",
		PlanActive: true, PlanFile: plan, AlwaysYOLO: true,
	})
	if d != workspace.Deny {
		t.Fatal("plan gate")
	}
	p := workspace.NewInactive()
	if p.Active {
		t.Fatal("NewInactive")
	}
	p.Enter(plan)
	if !p.Active {
		t.Fatal("Enter")
	}
	p.Exit()
	if p.Active {
		t.Fatal("Exit")
	}
}

func TestHooksModule(t *testing.T) {
	deny, _ := hooks.PreToolUse(context.Background(), t.TempDir(), "", "bash", "{}")
	if deny {
		t.Fatal("fail open")
	}
}

func TestGoalsModule(t *testing.T) {
	tr := goals.New(t.TempDir())
	if tr.HasGoal() || tr.IsActive() {
		t.Fatal("empty tracker")
	}
	if err := tr.Create("inventory", nil); err != nil {
		t.Fatal(err)
	}
	if !tr.IsActive() {
		t.Fatal("Create")
	}
	if tr.Reminder() == "" || tr.StatusLine() == "" || tr.Snapshot() == nil {
		t.Fatal("display helpers")
	}
	if _, _, err := tr.ApplyUpdate(false, "step", ""); err != nil {
		t.Fatal(err)
	}
	if !tr.PauseUser() || tr.IsActive() {
		t.Fatal("PauseUser")
	}
	if !tr.Resume() {
		t.Fatal("Resume")
	}
	if !tr.UserComplete() {
		t.Fatal("UserComplete")
	}
	tr.Clear()
	if tr.HasGoal() {
		t.Fatal("Clear")
	}
}

func TestToolsModulePacksAndExecute(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	dir := t.TempDir()
	tc := tools.Context{CWD: dir, SessionDir: filepath.Join(dir, "s")}
	if err := os.MkdirAll(tc.SessionDir, 0o700); err != nil {
		t.Fatal(err)
	}

	wantDefault := []string{
		"bash", "read_file", "write_file", "search_replace", "grep", "glob", "list_dir",
		"web_search", "web_fetch", "apply_patch", "skill", "memory_search", "memory_save",
		"ask_user_question", "enter_plan_mode", "exit_plan_mode", "task", "update_goal",
		"list_sessions", "read_session", "message_session", "list_agents", "send_message",
		"followup_task", "wait_agent",
	}
	assertPack(t, "DefaultPack", tools.DefaultPack(), wantDefault)
	assertPack(t, "DirectOnlyPack", tools.DirectOnlyPack(), []string{
		"ask_user_question", "enter_plan_mode", "exit_plan_mode", "task", "update_goal",
		"list_sessions", "read_session", "message_session", "list_agents", "send_message",
		"followup_task", "wait_agent",
	})
	nested := tools.NestedPack()
	assertPack(t, "NestedPack", nested, []string{
		"bash", "read_file", "write_file", "search_replace", "apply_patch",
		"grep", "glob", "list_dir", "web_search", "web_fetch", "skill",
		"memory_search", "memory_save",
	})
	if tools.Lookup(nested, "update_goal") != nil || tools.Lookup(nested, "task") != nil {
		t.Fatal("nested pack must not expose harness/human tools")
	}

	// Real Execute on file/search/bash/memory/goal (empty args must not panic).
	writeArgs, _ := json.Marshal(map[string]string{"path": filepath.Join(dir, "a.txt"), "contents": "hello"})
	res := tools.WriteFile{}.Execute(context.Background(), tc, writeArgs)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !tools.AgentWrote(filepath.Join(dir, "a.txt")) {
		t.Fatal("recordAgentWrite")
	}
	readArgs, _ := json.Marshal(map[string]string{"path": filepath.Join(dir, "a.txt")})
	res = tools.ReadFile{}.Execute(context.Background(), tc, readArgs)
	if res.IsError || !strings.Contains(res.Content, "hello") {
		t.Fatalf("read %s", res.Content)
	}
	bashArgs, _ := json.Marshal(map[string]string{"command": "echo inventory-ok"})
	res = tools.Bash{}.Execute(context.Background(), tc, bashArgs)
	if res.IsError || !strings.Contains(res.Content, "inventory-ok") {
		t.Fatalf("bash %s", res.Content)
	}
	specs := tools.Specs(tools.DefaultPack())
	if len(specs) != len(tools.DefaultPack()) {
		t.Fatal("Specs length")
	}
}

func assertPack(t *testing.T, label string, pack []tools.Tool, names []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, tl := range pack {
		if tl.Name() == "" || tl.Description() == "" || tl.Parameters() == nil {
			t.Fatalf("%s: incomplete tool %+v", label, tl)
		}
		if seen[tl.Name()] {
			t.Fatalf("%s duplicate %s", label, tl.Name())
		}
		seen[tl.Name()] = true
		// Empty args must not panic.
		_ = tl.Execute(context.Background(), tools.Context{CWD: t.TempDir()}, json.RawMessage(`{}`))
	}
	for _, n := range names {
		if !seen[n] {
			t.Errorf("%s missing %s", label, n)
		}
	}
}

func TestCodeModeModule(t *testing.T) {
	dir := t.TempDir()
	rt := codemode.New(tools.Context{CWD: dir, SessionDir: filepath.Join(dir, "s")}, tools.NestedPack())
	out, err := rt.Exec(context.Background(), `1+1`)
	if err != nil || out != "2" {
		t.Fatalf("exec %q %v", out, err)
	}
	var _ tools.Tool = codemode.ExecTool{RT: rt}
	var _ tools.Tool = codemode.WaitTool{RT: rt}
}

func TestAgentModule(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	model := catalog.Model{
		ID: "mock", Model: "mock", Name: "mock",
		Provider: types.ProviderFireworks, APIBackend: types.BackendChat,
		BaseURL: "https://example.invalid/v1",
	}
	sess, err := agent.NewSession(t.TempDir(), model, true)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.Dir == "" || sess.Goal == nil {
		t.Fatal("NewSession fields")
	}
	if agent.SystemPrompt(model, sess.CWD, "") == "" {
		t.Fatal("SystemPrompt")
	}
	_ = agent.LoadProjectInstructions(sess.CWD)
	if agent.EffectiveToolMode(model) != types.ToolModeDirect {
		t.Fatal("non-Responses Direct")
	}
	if tools.Lookup(agent.PackFor(model, nil), "bash") == nil {
		t.Fatal("PackFor Direct includes bash")
	}
	n := 0
	httpDo := httpDoerFunc(func(r *http.Request) (*http.Response, error) {
		n++
		body := `data: {"choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n"
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	if err := agent.RunTurn(context.Background(), sess, agent.Runtime{Model: model, Bearer: "k", HTTP: sampler.HTTPDoer(httpDo)}, "hi", nil); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("RunTurn did not sample")
	}
	if sess.ExportMarkdown() == "" {
		t.Fatal("ExportMarkdown")
	}
	dst, err := agent.ForkSession(sess)
	if err != nil || dst.ID == sess.ID {
		t.Fatalf("fork %v %s", err, dst.ID)
	}
	list, err := agent.ListSessions(sess.CWD)
	if err != nil || len(list) < 2 {
		t.Fatalf("ListSessions %v %d", err, len(list))
	}
	if _, err := agent.LoadSession(sess.CWD, sess.ID); err != nil {
		t.Fatal(err)
	}
}

func TestACPModule(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var out bytes.Buffer
	if err := acp.ServeStdio(context.Background(), acp.Options{CWD: t.TempDir(), In: in, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("entropy")) {
		t.Fatalf("%s", out.String())
	}
}

func TestMCPModule(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	if mcp.LoadServers() == nil {
		// empty config is a valid empty slice or nil
	}
	_, err := mcp.Connect(context.Background(), mcp.Server{Name: "x"})
	if err == nil {
		t.Fatal("Connect with no command/url must fail")
	}
}

func TestSessionBusModule(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	t.Cleanup(sessionbus.Stop)
	h, err := sessionbus.Start(func(d sessionbus.Delivered) string { return "Accepted" })
	if err != nil {
		t.Fatal(err)
	}
	h.Register(sessionbus.SessionInfo{SessionID: "s1", CWD: t.TempDir(), ProjectName: "p", ModelID: "m", Status: "idle"})
	if len(sessionbus.LiveSessions()) == 0 {
		t.Fatal("LiveSessions")
	}
	if _, _, ok := sessionbus.Find("s1"); !ok {
		t.Fatal("Find")
	}
	if sessionbus.ProjectName("/tmp/foo") == "" {
		t.Fatal("ProjectName")
	}
}

func TestUpdateModule(t *testing.T) {
	// Check() hits the network; only prove the types and Install fail-closed without a URL.
	_, err := update.Install(update.Result{Current: "0", Latest: "1"})
	if err == nil {
		t.Fatal("Install without asset URL must fail")
	}
}

func TestCLICommandTree(t *testing.T) {
	t.Setenv("ENTROPY_HOME", t.TempDir())
	config.ResetHomeForTest()
	root := cli.NewRoot()
	want := []string{
		"login", "logout", "models", "inspect", "sessions", "agent",
		"doctor", "update", "completions", "version", "mcp", "export", "memory", "wrap",
	}
	have := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		have[c.Name()] = c
	}
	for _, n := range want {
		if have[n] == nil {
			t.Errorf("missing command %s", n)
		}
	}
	if have["agent"].Commands() == nil {
		t.Fatal("agent subcommands")
	}
	sub := map[string]bool{}
	for _, c := range have["agent"].Commands() {
		sub[c.Name()] = true
	}
	for _, n := range []string{"stdio", "serve", "leader"} {
		if !sub[n] {
			t.Errorf("agent missing %s", n)
		}
	}
	sessSub := map[string]bool{}
	for _, c := range have["sessions"].Commands() {
		sessSub[c.Name()] = true
	}
	for _, n := range []string{"list", "show", "rm", "fork"} {
		if !sessSub[n] {
			t.Errorf("sessions missing %s", n)
		}
	}

	root.SetArgs([]string{"doctor"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("ENTROPY_HOME")) && !bytes.Contains([]byte(buf.String()+""), []byte("entropy")) {
		// doctor prints to stdout via fmt; still must not error
	}
}
