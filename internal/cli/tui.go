package cli

import (
	"context"

	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/pager"
	"github.com/spf13/cobra"
)

func runTUI(ctx context.Context, dir string, model catalog.Model) error {
	return pager.Run(ctx, pager.Options{
		CWD:       dir,
		Model:     model,
		YOLO:      flagYOLO,
		Resume:    flagResume,
		Continue:  flagContinue,
		Dashboard: false,
	})
}

func newDashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Open the Agent Dashboard (live roster of top-level sessions)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := cwd()
			if err != nil {
				return err
			}
			model, err := selectedModel()
			if err != nil {
				return err
			}
			return pager.Run(cmd.Context(), pager.Options{
				CWD:       dir,
				Model:     model,
				YOLO:      flagYOLO,
				Dashboard: true,
			})
		},
	}
}
