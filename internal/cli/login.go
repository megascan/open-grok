package cli

import (
	"fmt"
	"os"

	"github.com/megascan/open-grok/internal/auth"
	"github.com/megascan/open-grok/internal/config"
	"github.com/megascan/open-grok/internal/oauth"
	"github.com/megascan/open-grok/internal/types"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var (
		codex      bool
		apiKey     string
		kimiCode   bool
		deviceAuth bool
		useOAuth   bool
	)
	cmd := &cobra.Command{
		Use:   "login [provider]",
		Short: "Sign in to a provider (API key; Codex defaults to ChatGPT OAuth)",
		ValidArgs: func() []string {
			out := make([]string, 0, len(types.AllProviders))
			for _, p := range types.AllProviders {
				out = append(out, p.CLIName())
			}
			return out
		}(),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "xai"
			if len(args) > 0 {
				name = args[0]
			}
			if codex {
				name = "codex"
			}
			p, ok := types.ParseProvider(name)
			if !ok {
				return fmt.Errorf("unknown provider %q — try xai, codex, kimi, fireworks, deepseek, meta, wafer, zai, runinfra, gemini, opencode-go, openrouter", name)
			}
			if name == "kimi-code" || name == "kimi_code" {
				kimiCode = true
			}
			if p == types.ProviderCodex && apiKey == "" {
				ep := oauth.DefaultEndpoints()
				var tok oauth.TokenResponse
				var err error
				if deviceAuth {
					tok, err = oauth.DeviceLogin(cmd.Context(), ep)
				} else {
					tok, err = oauth.BrowserLogin(cmd.Context(), ep, true)
				}
				if err != nil {
					return err
				}
				if err := auth.SaveCodexOAuth(tok); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "saved OpenAI Codex OAuth tokens to %s\n", config.CodexAuthJSON())
				return nil
			}
			_ = useOAuth
			key, err := readAPIKey(p, apiKey)
			if err != nil {
				return err
			}
			if key == "" {
				return fmt.Errorf("no API key provided for %s", p.DisplayName())
			}
			if err := auth.SaveAPIKey(p, key, kimiCode); err != nil {
				return err
			}
			path := config.AuthJSON()
			if p == types.ProviderCodex {
				path = config.CodexAuthJSON()
			}
			fmt.Fprintf(os.Stderr, "saved %s credentials to %s (scope %s)\n", p.DisplayName(), path, auth.ScopeFor(p, kimiCode))
			return nil
		},
	}
	cmd.Flags().BoolVar(&codex, "codex", false, "Sign in to ChatGPT Codex (isolated store)")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key (otherwise prompt or env; skips OAuth)")
	cmd.Flags().BoolVar(&kimiCode, "kimi-code", false, "Store the key on the Kimi Code scope")
	cmd.Flags().BoolVar(&deviceAuth, "device-auth", false, "Codex device-code login (headless/remote)")
	cmd.Flags().BoolVar(&deviceAuth, "device-code", false, "Alias for --device-auth")
	cmd.Flags().BoolVar(&useOAuth, "oauth", false, "Use browser OAuth (Codex default when no --api-key)")
	return cmd
}

func newLogoutCmd() *cobra.Command {
	var (
		codex bool
		all   bool
	)
	cmd := &cobra.Command{
		Use:   "logout [provider]",
		Short: "Sign out and clear cached credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				return auth.Logout(types.ProviderXAI, true)
			}
			p := types.ProviderXAI
			if codex {
				p = types.ProviderCodex
			} else if len(args) > 0 {
				parsed, ok := types.ParseProvider(args[0])
				if !ok {
					return fmt.Errorf("unknown provider %q", args[0])
				}
				p = parsed
			}
			return auth.Logout(p, false)
		},
	}
	cmd.Flags().BoolVar(&codex, "codex", false, "Sign out of Codex only")
	cmd.Flags().BoolVar(&all, "all", false, "Sign out of every provider")
	return cmd
}
