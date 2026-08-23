package cli

import (
	"encoding/json"
	"fmt"

	"github.com/megascan/open-grok/internal/acp"
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/version"
	"github.com/spf13/cobra"
)

func newModelsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List available models and exit",
		RunE: func(cmd *cobra.Command, args []string) error {
			models := catalog.All()
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(models)
			}
			w := cmd.OutOrStdout()
			for _, m := range models {
				fmt.Fprintf(w, "%-28s  %-16s  %-18s  %s\n", m.ID, m.Provider, m.APIBackend, m.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return cmd
}

func newInspectCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Show the configuration Open Grok discovers for this directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			cfg, _ := config.Load()
			info := map[string]any{
				"version":       version.String(),
				"runtime":       "go",
				"cwd":           dir,
				"opengrok_home": config.Home(),
				"auth_json":     config.AuthJSON(),
				"codex_auth":    config.CodexAuthJSON(),
				"config_toml":   config.ConfigTOML(),
				"model":         cfg.Model,
				"system_config": config.SystemConfigDir(),
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
			}
			w := cmd.OutOrStdout()
			for _, k := range []string{"version", "runtime", "cwd", "opengrok_home", "auth_json", "codex_auth", "config_toml"} {
				fmt.Fprintf(w, "%s: %v\n", k, info[k])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return cmd
}

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run Open Grok without the interactive UI",
	}
	stdio := &cobra.Command{
		Use:   "stdio",
		Short: "ACP over stdio (IDE / headless clients)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			model, err := selectedModel()
			if err != nil {
				return err
			}
			return acp.ServeStdio(cmd.Context(), acp.Options{
				CWD:   dir,
				Model: model.ID,
				YOLO:  flagYOLO,
			})
		},
	}
	serve := &cobra.Command{
		Use:   "serve",
		Short: "WebSocket ACP server (127.0.0.1:2419)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("agent serve is not yet wired in the Go runtime; use `open-grok agent stdio`")
		},
	}
	leader := &cobra.Command{
		Use:   "leader",
		Short: "Shared leader process for other clients",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("agent leader is not yet wired in the Go runtime; use in-process TUI or agent stdio")
		},
	}
	cmd.AddCommand(stdio, serve, leader)
	return cmd
}
