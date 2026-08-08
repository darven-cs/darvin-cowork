package protocol

import "sync"

// APIKind names the wire protocol a ModelDescriptor is bound to.
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
// provider; each provider maps it to its native field.
type ThinkingLevel string

const (
	ThinkingOff    ThinkingLevel = "off"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
	ThinkingMax    ThinkingLevel = "max"
)

// ModelCost holds per-million-token pricing components in USD.
type ModelCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// Compat flags provider-specific capabilities consumed by higher layers.
type Compat struct {
	SupportsToolCalls      bool
	SupportsImageInput     bool
	SupportsUsageInStream  bool
	SupportsStrictToolMode bool
}

// ModelDescriptor is the static metadata for a specific model instance.
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
// reads from it.
type ModelRegistry struct {
	mu     sync.RWMutex
	byID   map[string]ModelDescriptor
	byProv map[string][]string
}

// NewModelRegistry returns an empty registry; tests use it to isolate
// state, production uses DefaultModelRegistry.
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		byID:   map[string]ModelDescriptor{},
		byProv: map[string][]string{},
	}
}

// DefaultModelRegistry is the global registry populated by provider init().
var DefaultModelRegistry = NewModelRegistry()

// RegisterModel adds m to the registry. Duplicate IDs panic.
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
// from init() blocks.
func (r *ModelRegistry) MustRegisterModel(m ModelDescriptor) {
	r.RegisterModel(m)
}

// Get returns the model descriptor for id and whether it exists.
func (r *ModelRegistry) Get(id string) (ModelDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.byID[id]
	return m, ok
}

// ListByProvider returns every model registered against the given
// provider name, in registration order.
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

// All returns every registered model; order is not specified.
func (r *ModelRegistry) All() []ModelDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ModelDescriptor, 0, len(r.byID))
	for _, m := range r.byID {
		out = append(out, m)
	}
	return out
}