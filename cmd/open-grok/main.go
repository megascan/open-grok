// Command open-grok is the native Go composition root for Open Grok.
//
// It wires the pager TUI, headless agent, ACP transports, and CLI onto the
// shared config, auth, sampler, tools, and workspace packages. User state
// lives only under $OPENGROK_HOME or ~/.opengrok — never ~/.grok.
package main

import (
	"fmt"
	"os"

	"github.com/megascan/open-grok/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
