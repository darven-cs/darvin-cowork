// ArtifactHook scans the accumulated assistant text at each turn end and
// emits ArtifactEvents (local services, file references, remote images)
// for the renderer's artifact panel. Workdir relativises file paths.

package agent

import (
	"strings"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/artifactdetect"
	"darvin-cowork/backend/internal/agents/event"
)

// ArtifactHook subscribes to the agent event bus and mirrors the
// TextDeltaHook attach pattern. State is reset per run so dedup never
// leaks across prompts.
type ArtifactHook struct {
	workdir string
	logger  *zap.Logger
	sub     *event.Subscription

	runID   string
	buf     strings.Builder
	msgID   string
	emitted map[string]bool
}

// NewArtifactHook constructs a hook. A nil logger makes emit failures silent.
func NewArtifactHook(workdir string, log *zap.Logger) *ArtifactHook {
	return &ArtifactHook{workdir: workdir, logger: log, emitted: map[string]bool{}}
}

// Attach subscribes to the Agent's event bus. Idempotent.
func (h *ArtifactHook) Attach(a *Agent) {
	if h.sub != nil {
		return
	}
	sub := a.Subscribe(128)
	h.sub = sub
	go func() {
		for ev := range sub.C() {
			switch e := ev.(type) {
			case event.RunStartEvent:
				h.reset(e.Common().RunID)
			case event.TextDeltaEvent:
				if e.SessionID != a.Session().ID {
					continue
				}
				h.buf.WriteString(e.Delta)
				if e.Common().MessageID != "" {
					h.msgID = e.Common().MessageID
				}
			case event.TurnEndEvent:
				h.flush(a, e.Common().RunID, e.Common().MessageID)
			}
		}
	}()
}

// Close unsubscribes, letting the drain goroutine exit. Called from
// SessionRuntime.Close on evict.
func (h *ArtifactHook) Close() {
	if h.sub != nil {
		h.sub.Unsubscribe()
		h.sub = nil
	}
}

func (h *ArtifactHook) reset(runID string) {
	if runID == h.runID {
		return
	}
	h.runID = runID
	h.buf.Reset()
	h.msgID = ""
	h.emitted = map[string]bool{}
}

func (h *ArtifactHook) flush(a *Agent, runID, msgID string) {
	text := h.buf.String()
	h.buf.Reset()
	if strings.TrimSpace(text) == "" {
		return
	}
	if msgID != "" {
		h.msgID = msgID
	}
	for _, d := range artifactdetect.Scan(text, h.workdir) {
		key := d.Kind + "|" + d.URL + "|" + d.FilePath
		if h.emitted[key] {
			continue
		}
		h.emitted[key] = true
		source := d.URL
		if source == "" {
			source = d.FilePath
		}
		a.Emit(event.ArtifactEvent{
			EventBase: event.EventBase{EventCommon: event.EventCommon{
				SessionID: a.Session().ID,
				RunID:     runID,
				MessageID: h.msgID,
			}},
			ArtifactID: artifactdetect.ArtifactID(source),
			Kind:       d.Kind,
			Name:       d.Name,
			Content:    d.Content,
			FilePath:   d.FilePath,
			URL:        d.URL,
			CreatedAt:  time.Now().UnixMilli(),
		})
	}
}
