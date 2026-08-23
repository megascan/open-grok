// Package cli is the Cobra command surface for the Open Grok Go binary.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	flagCWD      string
	flagModel    string
	flagProvider string
	flagYOLO     bool
	flagPrompt   string
	flagResume   string
	flagContinue bool
	flagDebug    bool
)

// Execute runs the root command.
func Execute() error {
	return NewRoot().Execute()
}

// NewRoot builds the open-grok command tree.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "open-grok",
		Short:         "Open Grok with ChatGPT Codex optimizations",
		Long:          "Open Grok is a native Go coding agent. User state lives in $OPENGROK_HOME or ~/.opengrok.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
		RunE:          runRoot,
	}
	root.PersistentFlags().StringVar(&flagCWD, "cwd", "", "Working directory")
	root.PersistentFlags().BoolVar(&flagDebug, "debug", false, "Enable debug logging")
	root.PersistentFlags().BoolVar(&flagYOLO, "always-approve", false, "Auto-approve all tool executions")
	root.PersistentFlags().BoolVarP(&flagYOLO, "yolo", "y", false, "Auto-approve all tool executions")
	root.PersistentFlags().StringVarP(&flagModel, "model", "m", "", "Model ID")
	root.PersistentFlags().StringVar(&flagProvider, "provider", "", "Provider identity when the model id is not in the catalog (never guessed from a URL)")
	root.Flags().StringVarP(&flagPrompt, "single", "p", "", "Headless single-turn prompt")
	root.Flags().Lookup("single").Shorthand = "p"
	root.Flags().StringVarP(&flagResume, "resume", "r", "", "Resume a session by id")
	root.Flags().BoolVarP(&flagContinue, "continue", "c", false, "Continue the most recent session")
	root.SetVersionTemplate("{{.Version}}\n")

	root.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newModelsCmd(),
		newInspectCmd(),
		newSessionsCmd(),
		newAgentCmd(),
		newDoctorCmd(),
		newUpdateCmd(),
		newCompletionsCmd(),
		newVersionCmd(),
		newMCPCmd(),
		newExportCmd(),
		newMemoryCmd(),
		newWrapCmd(),
	)
	return root
}

func cwd() (string, error) {
	if flagCWD != "" {
		return filepath.Abs(flagCWD)
	}
	return os.Getwd()
}

func selectedModel() (catalog.Model, error) {
	id := flagModel
	if id == "" {
		cfg, _ := config.Load()
		id = cfg.Model
	}
	if id == "" {
		id = catalog.DefaultID()
	}
	if m, ok := catalog.Lookup(id); ok {
		if flagProvider != "" {
			p, pok := types.ParseProvider(flagProvider)
			if !pok {
				return catalog.Model{}, fmt.Errorf("unknown provider %q", flagProvider)
			}
			if m.Provider != p {
				return catalog.Model{}, fmt.Errorf("model %s belongs to %s, not %s (identity comes from catalog metadata)", id, m.Provider, p)
			}
		}
		return m, nil
	}
	if m, ok := catalog.LookupIn(id, liveOverlay()); ok {
		return m, nil
	}
	if flagProvider == "" {
		return catalog.Model{}, fmt.Errorf("unknown model %q — pass --provider (e.g. openrouter) or use `open-grok models --live`", id)
	}
	p, ok := types.ParseProvider(flagProvider)
	if !ok {
		return catalog.Model{}, fmt.Errorf("unknown provider %q", flagProvider)
	}
	tools := true
	if strings.Contains(strings.ToLower(id), ":free") {
		tools = false
	}
	return catalog.Model{
		ID:            id,
		Model:         id,
		Name:          id,
		Provider:      p,
		APIBackend:    types.BackendChat,
		BaseURL:       catalog.DefaultBaseURL(p),
		EnvKey:        envKeyFor(p),
		ToolMode:      types.ToolModeDirect,
		SupportsTools: &tools,
	}, nil
}

func envKeyFor(p types.Provider) string {
	keys := auth.EnvKeys(p)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func liveOverlay() []catalog.Model {
	p := types.ProviderOpenRouter
	if flagProvider != "" {
		if parsed, ok := types.ParseProvider(flagProvider); ok {
			p = parsed
		}
	}
	if p != types.ProviderOpenRouter {
		return nil
	}
	bearer, _, err := auth.ResolveBearer(types.ProviderOpenRouter, auth.EnvKeys(types.ProviderOpenRouter), false)
	if err != nil || bearer == "" {
		return nil
	}
	c := catalog.LiveClient{}
	list, err := c.FetchOpenAIModels(types.ProviderOpenRouter, catalog.DefaultBaseURL(types.ProviderOpenRouter), bearer)
	if err != nil {
		return nil
	}
	return list
}

func runRoot(cmd *cobra.Command, args []string) error {
	if v, _ := cmd.Flags().GetBool("version"); v {
		fmt.Println(version.Display())
		return nil
	}
	dir, err := cwd()
	if err != nil {
		return err
	}
	model, err := selectedModel()
	if err != nil {
		return err
	}
	if flagPrompt != "" {
		return runHeadless(cmd.Context(), dir, model, flagPrompt)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "open-grok: no TTY; pass -p for headless or use `open-grok agent stdio`")
		return nil
	}
	return runTUI(cmd.Context(), dir, model)
}

func runHeadless(ctx context.Context, dir string, model catalog.Model, prompt string) error {
	kimiCode := model.EnvKey == "KIMI_CODE_API_KEY"
	bearer, _, err := auth.ResolveBearer(model.Provider, auth.EnvKeys(model.Provider), kimiCode)
	if err != nil {
		return err
	}
	var sess *agent.Session
	switch {
	case flagResume != "":
		sess, err = agent.LoadSession(dir, flagResume)
	case flagContinue:
		list, lerr := agent.ListSessions(dir)
		if lerr != nil {
			return lerr
		}
		if len(list) == 0 {
			sess, err = agent.NewSession(dir, model, flagYOLO)
		} else {
			s := list[0]
			sess = &s
			sess.YOLO = flagYOLO
		}
	default:
		sess, err = agent.NewSession(dir, model, flagYOLO)
	}
	if err != nil {
		return err
	}
	sess.YOLO = flagYOLO
	var out strings.Builder
	err = agent.RunTurn(ctx, sess, agent.Runtime{Model: model, Bearer: bearer}, prompt, func(ev agent.Event) {
		if ev.Kind == "text" {
			fmt.Fprint(os.Stdout, ev.Text)
			out.WriteString(ev.Text)
		}
	})
	if err != nil {
		return err
	}
	if !strings.HasSuffix(out.String(), "\n") {
		fmt.Fprintln(os.Stdout)
	}
	return nil
}

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				return enc.Encode(map[string]any{
					"name":    "open-grok",
					"version": version.String(),
					"runtime": "go",
					"goos":    runtime.GOOS,
					"goarch":  runtime.GOARCH,
				})
			}
			fmt.Fprintln(cmd.OutOrStdout(), version.Display())
			fmt.Fprintf(cmd.OutOrStdout(), "go %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Machine-readable JSON")
	return cmd
}

func readAPIKey(provider types.Provider, flagKey string) (string, error) {
	if flagKey != "" {
		return flagKey, nil
	}
	for _, name := range auth.EnvKeys(provider) {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	fmt.Fprintf(os.Stderr, "API key for %s: ", provider.DisplayName())
	byteKey, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		// Fallback for environments where ReadPassword fails.
		sc := bufio.NewScanner(os.Stdin)
		if sc.Scan() {
			return strings.TrimSpace(sc.Text()), nil
		}
		return "", err
	}
	return strings.TrimSpace(string(byteKey)), nil
}
