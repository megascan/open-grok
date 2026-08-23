package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List sessions for this directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			list, err := agent.ListSessions(dir)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			type row struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Model    string `json:"model"`
				Provider string `json:"provider"`
				Updated  string `json:"updated_at"`
			}
			var rows []row
			for _, s := range list {
				rows = append(rows, row{s.ID, s.Title, s.ModelID, string(s.Provider), s.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")})
			}
			return enc.Encode(rows)
		},
	}
	return cmd
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check terminal, home, and provider credential availability",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("open-grok %s (go %s %s/%s)\n", version.String(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
			fmt.Printf("OPENGROK_HOME=%s\n", config.Home())
			fmt.Printf("tty stdin=%v stdout=%v\n", term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd())))
			st := auth.Status()
			for _, p := range types.AllProviders {
				mark := "signed out"
				if s, ok := st[p]; ok {
					mark = s
				}
				fmt.Printf("  %-16s %s\n", p.CLIName(), mark)
			}
			if config.NeverGrokHome(config.Home()) {
				fmt.Println("ERROR: home resolved to ~/.grok — this is forbidden")
			}
			return nil
		},
	}
}

func newCompletionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completions [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := NewRoot()
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(out)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			case "powershell":
				return root.GenPowerShellCompletion(out)
			default:
				return fmt.Errorf("unknown shell %s", args[0])
			}
		},
	}
}

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP server configurations in ~/.opengrok/config.toml",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if len(cfg.MCP) == 0 {
				fmt.Println("(no mcp_servers in config.toml)")
				return nil
			}
			return json.NewEncoder(os.Stdout).Encode(cfg.MCP)
		},
	})
	return cmd
}
