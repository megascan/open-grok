// Package agent runs the Entropy turn loop: sample → plan gate → permissions → tools.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/version"
)

// SystemPrompt builds the model-facing instructions for a Direct-tool session.
func SystemPrompt(model catalog.Model, cwd string, extra string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are Entropy, a native Go coding agent (version %s) running on %s/%s.\n", version.String(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "You are not affiliated with xAI or OpenAI. User state lives only under $ENTROPY_HOME or ~/.entropy — never ~/.grok.\n")
	fmt.Fprintf(&b, "Model: %s (%s) via provider %s (%s).\n", model.Name, model.Model, model.Provider.DisplayName(), model.Provider)
	fmt.Fprintf(&b, "Workspace: %s\n", cwd)
	fmt.Fprintf(&b, "Today: %s\n\n", time.Now().Format("Monday, 2 January 2006"))
	b.WriteString(`You help the user with software engineering in this workspace.

Tools:
- bash: run shell commands. Prefer it for git, tests, and builds.
- read_file, write_file, search_replace, list_dir, grep, glob: inspect and edit files.
- web_search / web_fetch: public web (client-side fallback; do not assume xAI or OpenAI hosted search).
- enter_plan_mode / exit_plan_mode: plan mode is a hard edit gate. While it is active only session plan.md may be edited. Non-edit tools still run.
- update_goal: report progress when a /goal is Active. completed:true records a claim and does not end the goal; the user confirms with /goal complete. blocked_reason is a failure signal after 3+ consecutive failed attempts.

Rules:
- Provider identity comes from model metadata, never from a slug or URL.
- Do not mix credentials, catalogs, hosted tools, or opaque history across providers.
- Record every file write through the file tools (hunks must be attributed to the agent).
- Plan mode is not permission YOLO.
- Be concise. Lead with the answer. Use complete sentences.
- Do not invent file contents; read before editing.
`)
	if extra != "" {
		b.WriteString("\nProject instructions:\n")
		b.WriteString(extra)
		b.WriteString("\n")
	}
	return b.String()
}

// LoadProjectInstructions discovers AGENTS.md walking from cwd to root.
func LoadProjectInstructions(cwd string) string {
	var chunks []string
	dir := cwd
	for i := 0; i < 12; i++ {
		for _, name := range []string{"AGENTS.md", "agents.md"} {
			p := filepath.Join(dir, name)
			data, err := os.ReadFile(p)
			if err == nil && len(data) > 0 {
				chunks = append(chunks, fmt.Sprintf("# %s\n%s", p, string(data)))
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Closest file last so it wins in concatenation order for the model.
	for i, j := 0, len(chunks)-1; i < j; i, j = i+1, j-1 {
		chunks[i], chunks[j] = chunks[j], chunks[i]
	}
	return strings.Join(chunks, "\n\n")
}
