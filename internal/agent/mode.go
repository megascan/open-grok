package agent

import (
	"github.com/megascan/open-grok/internal/catalog"
	"github.com/megascan/open-grok/internal/codemode"
	"github.com/megascan/open-grok/internal/tools"
	"github.com/megascan/open-grok/internal/types"
)

// EffectiveToolMode applies the Code Mode contract:
// Codex code_mode_only on Responses wins; non-Responses fails closed to Direct.
func EffectiveToolMode(m catalog.Model) types.ToolMode {
	if m.APIBackend != types.BackendResponses {
		return types.ToolModeDirect
	}
	if m.Provider == types.ProviderCodex && m.ToolMode == types.ToolModeCodeModeOnly {
		return types.ToolModeCodeModeOnly
	}
	if m.ToolMode == "" {
		return types.ToolModeDirect
	}
	return m.ToolMode
}

// PackFor returns the model-facing tool list for a session.
func PackFor(m catalog.Model, js *codemode.Runtime) []tools.Tool {
	switch EffectiveToolMode(m) {
	case types.ToolModeCodeModeOnly:
		pack := tools.DirectOnlyPack()
		if js != nil {
			pack = append([]tools.Tool{codemode.ExecTool{RT: js}, codemode.WaitTool{RT: js}}, pack...)
		}
		return pack
	case types.ToolModeCodeMode:
		pack := tools.DefaultPack()
		if js != nil {
			pack = append([]tools.Tool{codemode.ExecTool{RT: js}, codemode.WaitTool{RT: js}}, pack...)
		}
		return pack
	default:
		return tools.DefaultPack()
	}
}
