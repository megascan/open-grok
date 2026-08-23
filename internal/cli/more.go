package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/megascan/open-grok/internal/agent"
	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
	"github.com/megascan/open-grok/internal/version"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newSessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List, show, fork, or delete sessions for this directory",
		RunE:  runSessionsList,
	}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List sessions", RunE: runSessionsList})
	cmd.AddCommand(&cobra.Command{
		Use:   "show [id]",
		Short: "Print a session summary",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			sess, err := agent.LoadSession(dir, args[0])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"id": sess.ID, "title": sess.Title, "model": sess.ModelID,
				"provider": sess.Provider, "messages": len(sess.Messages),
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "rm [id]",
		Short: "Delete a session directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			sess, err := agent.LoadSession(dir, args[0])
			if err != nil {
				return err
			}
			return os.RemoveAll(sess.Dir)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "fork [id]",
		Short: "Copy a session into a new id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			src, err := agent.LoadSession(dir, args[0])
			if err != nil {
				return err
			}
			dst, err := agent.ForkSession(src)
			if err != nil {
				return err
			}
			fmt.Println(dst.ID)
			return nil
		},
	})
	return cmd
}

func runSessionsList(cmd *cobra.Command, args []string) error {
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
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check terminal, home, and provider credential availability",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("entropy %s (go %s %s/%s)\n", version.String(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
			fmt.Printf("ENTROPY_HOME=%s\n", config.Home())
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
		Short: "Manage MCP server configurations in ~/.entropy/config.toml",
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
	cmd.AddCommand(&cobra.Command{
		Use:   "add [name] -- [command] [args...]",
		Short: "Add an MCP stdio server to config.toml",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.MCP == nil {
				cfg.MCP = map[string]config.MCP{}
			}
			cfg.MCP[args[0]] = config.MCP{Command: args[1], Args: args[2:]}
			return config.Save(cfg)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "remove [name]",
		Short: "Remove an MCP server from config.toml",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			delete(cfg.MCP, args[0])
			return config.Save(cfg)
		},
	})
	return cmd
}

func newExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export [session-id]",
		Short: "Export a session transcript as Markdown",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			id := ""
			if len(args) > 0 {
				id = args[0]
			} else {
				list, err := agent.ListSessions(dir)
				if err != nil {
					return err
				}
				if len(list) == 0 {
					return fmt.Errorf("no sessions")
				}
				id = list[0].ID
			}
			sess, err := agent.LoadSession(dir, id)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), sess.ExportMarkdown())
			return nil
		},
	}
}

func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage cross-session memory notes",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List memory notes",
		RunE: func(cmd *cobra.Command, args []string) error {
			res := tools.MemorySearch{}.Execute(cmd.Context(), tools.Context{}, json.RawMessage(`{"query":""}`))
			fmt.Fprintln(cmd.OutOrStdout(), res.Content)
			return nil
		},
	})
	add := &cobra.Command{
		Use:   "add [name]",
		Short: "Write a memory note (content from --content or stdin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			content, _ := cmd.Flags().GetString("content")
			if content == "" {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				content = string(data)
			}
			body, _ := json.Marshal(map[string]string{"name": args[0], "content": content})
			res := tools.MemorySave{}.Execute(cmd.Context(), tools.Context{}, body)
			fmt.Fprintln(cmd.OutOrStdout(), res.Content)
			if res.IsError {
				return fmt.Errorf("%s", res.Content)
			}
			return nil
		},
	}
	add.Flags().String("content", "", "Note body")
	cmd.AddCommand(add)
	return cmd
}

func newWrapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wrap [command] [args...]",
		Short: "Run a command with this process's stdin/stdout",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := exec.Command(args[0], args[1:]...)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
}
