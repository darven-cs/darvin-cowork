// Capability declarations and the host-facility checks a harness
// advertises / a caller's context engine requires.

package harness

// ContextEngineHostCapability names one host-side facility a context
// engine may require.
type ContextEngineHostCapability string

const (
	HostBootstrap            ContextEngineHostCapability = "bootstrap"
	HostAssembleBeforePrompt ContextEngineHostCapability = "assemble-before-prompt"
	HostAfterTurn            ContextEngineHostCapability = "after-turn"
	HostMaintain             ContextEngineHostCapability = "maintain"
	HostCompact              ContextEngineHostCapability = "compact"
	HostRuntimeLLMComplete   ContextEngineHostCapability = "runtime-llm-complete"
)

// ContextEngineOperation is the operation whose host requirements are checked.
type ContextEngineOperation string

const (
	OpAgentRun ContextEngineOperation = "agent-run"
	OpCompact  ContextEngineOperation = "compact"
)

// LegacyContextEngineID exempts requirements from host capability checks.
const LegacyContextEngineID = "legacy"

// ContextEngineRequirement is what the caller's context engine needs from a
// harness for one operation.
type ContextEngineRequirement struct {
	EngineID  string
	Operation ContextEngineOperation
	// RequiredCapabilities must all be advertised by the harness.
	RequiredCapabilities []ContextEngineHostCapability
	// UnsupportedMessage overrides the generated rejection text.
	UnsupportedMessage string
}

// VisibleReplies is how a harness expects visible replies to be produced.
type VisibleReplies string

const (
	VisibleRepliesAutomatic   VisibleReplies = "automatic"
	VisibleRepliesMessageTool VisibleReplies = "message_tool"
)

// DeliveryDefaults is a harness's reply-delivery preference.
type DeliveryDefaults struct {
	VisibleReplies VisibleReplies
}

// Capability names one optional interface for declaration and verification.
type Capability string

const (
	CapCompact             Capability = "compact"
	CapClassify            Capability = "classify"
	CapSideQuestion        Capability = "sideQuestion"
	CapSessionFork         Capability = "sessionFork"
	CapFinalizeSettledTurn Capability = "finalizeSettledTurn"
	CapUsageSnapshot       Capability = "usageSnapshot"
)

// Capabilities is a harness's declaration of what it serves. The boolean
// fields must agree with the capability interfaces the type implements;
// VerifyCapabilities checks that.
type Capabilities struct {
	// Healthy gates the harness out of auto-selection when false.
	Healthy bool

	Compact             bool
	Classify            bool
	SideQuestion        bool
	SessionFork         bool
	FinalizeSettledTurn bool
	UsageSnapshot       bool

	// ContextEngineHost lists the host facilities this harness provides;
	// an unadvertised host fails closed (superset test).
	ContextEngineHost []ContextEngineHostCapability
	// DelegatedExecution lists plugin ids allowed to delegate here.
	DelegatedExecution []string
	// DeliveryDefaults is the reply-delivery preference; nil = undeclared.
	DeliveryDefaults *DeliveryDefaults
}

// Declares reports whether c claims the given capability.
func (c Capabilities) Declares(cap Capability) bool {
	switch cap {
	case CapCompact:
		return c.Compact
	case CapClassify:
		return c.Classify
	case CapSideQuestion:
		return c.SideQuestion
	case CapSessionFork:
		return c.SessionFork
	case CapFinalizeSettledTurn:
		return c.FinalizeSettledTurn
	case CapUsageSnapshot:
		return c.UsageSnapshot
	default:
		return false
	}
}

// MissingHostCapabilities returns the capabilities req demands that c does
// not advertise. A nil req / empty required set / legacy engine all return
// nothing; otherwise it is a superset test.
func (c Capabilities) MissingHostCapabilities(req *ContextEngineRequirement) []ContextEngineHostCapability {
	if req == nil || len(req.RequiredCapabilities) == 0 || req.EngineID == LegacyContextEngineID {
		return nil
	}
	var missing []ContextEngineHostCapability
	for _, want := range req.RequiredCapabilities {
		if !hasHostCapability(c.ContextEngineHost, want) {
			missing = append(missing, want)
		}
	}
	return missing
}

// AllowsDelegation reports whether pluginID may delegate execution here.
func (c Capabilities) AllowsDelegation(pluginID string) bool {
	if pluginID == "" {
		return true
	}
	return contains(c.DelegatedExecution, pluginID)
}

func hasHostCapability(list []ContextEngineHostCapability, want ContextEngineHostCapability) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
