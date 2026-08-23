package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/megascan/open-grok/internal/update"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var (
		check  bool
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for or install verified Open Grok GitHub releases",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := update.Check()
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(res)
			}
			fmt.Printf("current: %s\nlatest:  %s\nnewer:   %v\nasset:   %s\n", res.Current, res.Latest, res.Newer, res.Asset)
			if !check && res.Newer {
				fmt.Println("automatic install is not enabled in this Go build; download the asset and replace $OPENGROK_HOME/bin/open-grok")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Check only")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON output")
	return cmd
}
