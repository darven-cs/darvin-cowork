package protocol

import "sync"

// APIKind names the wire protocol a ModelDescriptor is bound to. Used to
// pick buildRequest / parseResponse / stream parsing at provider boundary.
type APIKind string

const (
	APIAnthropicMessages  APIKind = "anthropic-messages"
	APIOpenAICompletions  APIKind = "openai-completions"
	APIGeminiGenerativeAI APIKind = "google-generative-ai"
)

// InputModality enumerates the input shapes a model can ingest.
type InputModality string

const (
	InputText  InputModality = "text"
	InputImage InputModality = "image"
)

// ThinkingLevel is the unified reasoning-effort level accepted by every
// provider. Providers map it to their native field (budget_tokens,
// reasoning_effort, thinkingBudget) under the hood.
type ThinkingLevel string

const (
	ThinkingOff    ThinkingLevel = "off"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
	ThinkingMax    ThinkingLevel = "max"
)

// ModelCost holds per-million-token pricing components in USD. CacheRead
// is typically the Input rate discounted; CacheWrite is typically 1.25x
// Input for short-TTL caches.
type ModelCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// Compat flags provider-specific capabilities consumed by the higher
// layers (ContextEngine, executor) when deciding what to send.
type Compat struct {
	SupportsToolCalls      bool
	SupportsImageInput     bool
	SupportsUsageInStream  bool
	SupportsStrictToolMode bool
}

// ModelDescriptor is the static metadata for a specific model instance.
// It is registered at provider init() and consumed by the ContextEngine
// (contextWindow / MaxTokens), the budget tracker (Cost) and the executor
// (Compat) without any network call.
type ModelDescriptor struct {
	ID            string
	Name          string
	Provider      string
	APIVersion    APIKind
	ContextWindow int
	MaxTokens     int
	Reasoning     bool
	ThinkingMap   map[ThinkingLevel]string
	Input         []InputModality
	Cost          ModelCost
	Compat        Compat
}

// ModelRegistry is a process-wide lookup table for ModelDescriptor keyed
// by model ID. Providers populate it from init(); the rest of the agent
// reads from it (ContextEngine for contextWindow, Settings UI for the
// model picker, etc).
type ModelRegistry struct {
	mu     sync.RWMutex
	byID   map[string]ModelDescriptor
	byProv map[string][]string
}

// NewModelRegistry returns an empty registry. Tests use this to build
// isolated instances; production code uses DefaultModelRegistry.
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		byID:   map[string]ModelDescriptor{},
		byProv: map[string][]string{},
	}
}

// DefaultModelRegistry is the global registry populated by provider init()
// functions. Callers should use it unless they have a reason to isolate
// state (e.g. tests).
var DefaultModelRegistry = NewModelRegistry()

// RegisterModel adds m to the registry. Duplicate IDs panic — concurrent
// registrations of the same model usually indicate a copy-paste bug in
// the provider package, so failing fast is preferable.
func (r *ModelRegistry) RegisterModel(m ModelDescriptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[m.ID]; exists {
		panic("protocol: model " + m.ID + " already registered")
	}
	r.byID[m.ID] = m
	r.byProv[m.Provider] = append(r.byProv[m.Provider], m.ID)
}

// MustRegisterModel is the panic-on-duplicate convenience wrapper used
// from init() blocks. Functionally identical to RegisterModel.
func (r *ModelRegistry) MustRegisterModel(m ModelDescriptor) {
	r.RegisterModel(m)
}

// Get returns the model descriptor for id and whether it exists. Returns
// the zero ModelDescriptor and false for unknown IDs; callers decide
// whether to treat misses as errors.
func (r *ModelRegistry) Get(id string) (ModelDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.byID[id]
	return m, ok
}

// ListByProvider returns every model registered against the given
// provider name, in registration order. Unknown provider returns nil so
// callers can distinguish "no such provider" from "provider with no
// models registered".
func (r *ModelRegistry) ListByProvider(name string) []ModelDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids, ok := r.byProv[name]
	if !ok {
		return nil
	}
	out := make([]ModelDescriptor, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.byID[id])
	}
	return out
}

// All returns every registered model. Order is not specified.
func (r *ModelRegistry) All() []ModelDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ModelDescriptor, 0, len(r.byID))
	for _, m := range r.byID {
		out = append(out, m)
	}
	return out
}
