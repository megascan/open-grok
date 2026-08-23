// Command entropy is the native Go composition root for Entropy.
//
// It wires the pager TUI, headless agent, ACP transports, and CLI onto the
// shared config, auth, sampler, tools, and workspace packages. User state
// lives only under $ENTROPY_HOME or ~/.entropy — never ~/.grok.
package main

import (
	"fmt"
	"os"

	"github.com/megascan/open-grok/internal/cli"
	"github.com/megascan/open-grok/internal/pager"
)

func main() {
	if pager.TryMermaidRender(os.Args) {
		return
	}
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
