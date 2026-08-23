package cli

import (
	"context"

	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/pager"
)

func runTUI(ctx context.Context, dir string, model catalog.Model) error {
	return pager.Run(ctx, pager.Options{
		CWD:      dir,
		Model:    model,
		YOLO:     flagYOLO,
		Resume:   flagResume,
		Continue: flagContinue,
	})
}
