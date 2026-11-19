package helps

import (
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// ResolveAntigravityEffortVariant maps a model the credential auth
// registered under its own name, folded from effort-named ids, to the
// Antigravity catalogue id that serves it at the requested effort, as
// "gemini-4-hydrogen" to "gemini-4-hydrogen-xhigh". It decides from the
// models the credential registered, so the operator's exclusions and aliases
// govern it as they govern the model list. The effort comes from the model's
// thinking suffix or the request body in sourceFormat; a thinking suffix is
// carried over to the returned id. Any other model is returned unchanged.
func ResolveAntigravityEffortVariant(auth *cliproxyauth.Auth, model string, payload []byte, sourceFormat string) string {
	if auth == nil {
		return model
	}
	suffix := thinking.ParseSuffix(model)
	registered := registry.GetGlobalRegistry().GetModelsForClient(auth.ID)
	effort := thinking.ExtractReasoningEffort(payload, sourceFormat, model)
	variant, ok := registry.RegisteredEffortVariantFor(registered, suffix.ModelName, effort)
	if !ok {
		return model
	}
	if suffix.HasSuffix {
		return variant + "(" + suffix.RawSuffix + ")"
	}
	return variant
}
