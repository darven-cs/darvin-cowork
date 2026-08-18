// Package scheduledtask owns the cron-style timer engine that drives
// headless agent turns on a fixed schedule. The engine lives inside
// darvin-agent and shares sessions.db with the rest of the agent, so
// schedule state and session state survive the same restart.

package scheduledtask

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/store"
)

// Polling cadence and exponential backoff ladder; aligned with OpenClaw's
// retry policy in LobsterAI/src/scheduledTask/cronJobService.ts.
const (
	pollInterval = 30 * time.Second
	maxAttempts  = 6
	failureBadge = 5 // consecutiveErrors threshold for the UI badge
)

// backoffFor returns the wait time before attempt N (N starts at 2 for
// the retry after the first failure; attempt 6 failure auto-disables).
func backoffFor(attempt int) time.Duration {
	switch {
	case attempt <= 2:
		return 30 * time.Second
	case attempt == 3:
		return 1 * time.Minute
	case attempt == 4:
		return 5 * time.Minute
	case attempt == 5:
		return 15 * time.Minute
	default:
		return 60 * time.Minute
	}
}

// SessionRunner is the surface Engine needs from the session runtime.
// SubmitForSchedule enqueues a headless turn on the schedule's pinned
// session; Abort cancels the in-flight turn on that session. The impl
// is wired by runtime.Build (a thin adapter over gateway.SessionManager).
type SessionRunner interface {
	SubmitForSchedule(ctx context.Context, scheduleID, prompt string) (runID string, err error)
	Abort(ctx context.Context, scheduleID, runID string) error
	// IsRunActive reports whether runID is still the in-flight turn on
	// the schedule's pinned session. Engine reconciles "running" run rows
	// against this on every tick.
	IsRunActive(ctx context.Context, scheduleID, runID string) bool
}

// Broadcaster fans a notification out to every active WS client. The
// gateway's EventLedger implements this shape; Engine only depends on
// the interface so scheduledtask stays gateway-agnostic.
type Broadcaster interface {
	Broadcast(method string, params any)
}

// Engine owns the cron tick loop. One Engine per process.
type Engine struct {
	store    *store.ScheduleStore
	bcast    Broadcaster
	sessions SessionRunner
	log      *zap.Logger

	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
	runIDs  map[string]string // scheduleID -> active runID, used by Abort
	doneCh  chan struct{}
}

// NewEngine builds an Engine. bcast / sessions may be nil; the engine
// degrades to no-ops on the corresponding path until they are wired.
func NewEngine(s *store.ScheduleStore, bcast Broadcaster, sessions SessionRunner, log *zap.Logger) *Engine {
	return &Engine{
		store:    s,
		bcast:    bcast,
		sessions: sessions,
		log:      log,
		runIDs:   make(map[string]string),
	}
}

// Start launches the cron goroutine. Idempotent.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}
	c, cancel := context.WithCancel(ctx)
	e.ctx = c
	e.cancel = cancel
	e.running = true
	e.doneCh = make(chan struct{})
	go e.run(c)
	return nil
}

// Stop signals the cron goroutine to exit and waits up to ctx.
func (e *Engine) Stop(ctx context.Context) error {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return nil
	}
	e.cancel()
	done := e.doneCh
	e.running = false
	e.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) run(ctx context.Context) {
	defer close(e.doneCh)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	if err := e.tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		e.log.Warn("initial schedule tick failed", zap.Error(err))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				e.log.Warn("schedule tick failed", zap.Error(err))
			}
		}
	}
}

func (e *Engine) tick(ctx context.Context) error {
	now := time.Now().UnixMilli()
	due, err := e.store.SelectDue(ctx, now)
	if err != nil {
		return fmt.Errorf("select due: %w", err)
	}
	for i := range due {
		if err := e.trigger(ctx, &due[i]); err != nil {
			e.log.Warn("trigger failed",
				zap.String("schedule_id", due[i].ID),
				zap.Error(err))
		}
	}
	e.reconcileRunningRuns(ctx)
	return nil
}

// reconcileRunningRuns closes run rows whose turn is no longer the
// session's in-flight run. The engine does not subscribe to per-session
// event buses (they are owned by the gateway); polling the active-run
// check on every tick bounds the close latency to one tick.
func (e *Engine) reconcileRunningRuns(ctx context.Context) {
	runs, err := e.store.ListRunningRuns(ctx)
	if err != nil {
		e.log.Warn("list running runs failed", zap.Error(err))
		return
	}
	for _, run := range runs {
		if run.RunID == nil || *run.RunID == "" {
			continue
		}
		if e.sessions == nil {
			continue
		}
		if e.sessions.IsRunActive(ctx, run.ScheduleID, *run.RunID) {
			continue
		}
		now := time.Now().UnixMilli()
		_ = e.store.UpdateRun(ctx, run.ID, map[string]any{
			"status":   "done",
			"ended_at": now,
		})
		e.log.Info("run reconciled to done",
			zap.String("run_id", run.ID),
			zap.Int64("ended_at", now))
		e.emitRunsChanged(run.ScheduleID, run.ID)
	}
}

// trigger fires one schedule. It computes next fire first, persists it,
// then issues the headless turn so a crash mid-run cannot replay the
// schedule on restart. Failure applies the backoff ladder.
func (e *Engine) trigger(ctx context.Context, sched *store.Schedule) error {
	body, err := store.DecodeScheduleBody(sched.ScheduleJSON)
	if err != nil {
		return fmt.Errorf("decode schedule body: %w", err)
	}
	next, err := computeNextFire(body, time.Now())
	if err != nil {
		return fmt.Errorf("compute next fire: %w", err)
	}

	run := &store.ScheduleRun{
		ID:          newScheduleID(),
		ScheduleID:  sched.ID,
		TriggeredAt: time.Now().UnixMilli(),
		TriggerKind: "scheduled",
		Status:      "running",
		Attempts:    1,
	}
	if err := e.store.CreateRun(ctx, run); err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	runID, err := e.sessions.SubmitForSchedule(ctx, sched.ID, sched.Prompt)
	if err != nil {
		now := time.Now().UnixMilli()
		errMsg := err.Error()
		_ = e.store.UpdateRun(ctx, run.ID, map[string]any{
			"status": "failed", "error": errMsg, "ended_at": now,
		})
		backoffMs := time.Now().Add(backoffFor(1)).UnixMilli()
		_ = e.store.IncrementConsecutiveErrors(ctx, sched.ID, backoffMs)
		if sched.ConsecutiveErrors+1 >= maxAttempts {
			if _, derr := e.store.Toggle(ctx, sched.ID, false); derr != nil {
				e.log.Warn("auto-disable failed", zap.String("schedule_id", sched.ID), zap.Error(derr))
			}
			e.log.Warn("schedule auto-disabled after max attempts",
				zap.String("schedule_id", sched.ID))
		}
		e.emitRunsChanged(sched.ID, run.ID)
		return fmt.Errorf("submit: %w", err)
	}

	if err := e.store.MarkFired(ctx, sched.ID, next.UnixMilli()); err != nil {
		e.log.Warn("mark fired failed", zap.String("schedule_id", sched.ID), zap.Error(err))
	}
	_ = e.store.ResetConsecutiveErrors(ctx, sched.ID)
	_ = e.store.UpdateRun(ctx, run.ID, map[string]any{
		"session_id": sessionIDFor(sched.ID),
		"run_id":     runID,
	})

	e.mu.Lock()
	e.runIDs[sched.ID] = run.ID
	e.mu.Unlock()

	e.emitFired(sched.ID, run.ID, run.TriggeredAt)
	e.emitRunsChanged(sched.ID, run.ID)
	return nil
}

// TriggerNow fires a schedule immediately, ignoring enabled and the
// tick loop. Backs the agent.schedule.run_now RPC.
func (e *Engine) TriggerNow(ctx context.Context, scheduleID string) (store.ScheduleRun, error) {
	sched, err := e.store.Get(ctx, scheduleID)
	if err != nil {
		return store.ScheduleRun{}, err
	}
	run := &store.ScheduleRun{
		ID:          newScheduleID(),
		ScheduleID:  sched.ID,
		TriggeredAt: time.Now().UnixMilli(),
		TriggerKind: "manual",
		Status:      "running",
		Attempts:    1,
	}
	if err := e.store.CreateRun(ctx, run); err != nil {
		return store.ScheduleRun{}, err
	}
	runID, err := e.sessions.SubmitForSchedule(ctx, sched.ID, sched.Prompt)
	if err != nil {
		now := time.Now().UnixMilli()
		errMsg := err.Error()
		_ = e.store.UpdateRun(ctx, run.ID, map[string]any{
			"status": "failed", "error": errMsg, "ended_at": now,
		})
		return *run, err
	}
	_ = e.store.UpdateRun(ctx, run.ID, map[string]any{
		"session_id": sessionIDFor(sched.ID),
		"run_id":     runID,
	})
	e.mu.Lock()
	e.runIDs[sched.ID] = run.ID
	e.mu.Unlock()
	e.emitFired(sched.ID, run.ID, run.TriggeredAt)
	e.emitRunsChanged(sched.ID, run.ID)
	return *run, nil
}

// Abort cancels the in-flight run, if any, and stamps the row.
func (e *Engine) Abort(ctx context.Context, scheduleID, runID string) (bool, error) {
	e.mu.Lock()
	active, has := e.runIDs[scheduleID]
	e.mu.Unlock()
	if !has || active != runID {
		return false, nil
	}
	if err := e.sessions.Abort(ctx, scheduleID, runID); err != nil {
		return false, fmt.Errorf("session abort: %w", err)
	}
	_ = e.store.UpdateRun(ctx, runID, map[string]any{
		"status":   "aborted",
		"ended_at": time.Now().UnixMilli(),
	})
	e.emitRunsChanged(scheduleID, runID)
	return true, nil
}

func computeNextFire(body store.ScheduleBody, now time.Time) (time.Time, error) {
	switch body.Kind {
	case "at":
		t, err := time.Parse(time.RFC3339, body.At)
		if err != nil {
			return time.Time{}, fmt.Errorf("at: %w", err)
		}
		return t, nil
	case "every":
		if body.EveryMs <= 0 {
			return time.Time{}, errors.New("everyMs must be > 0")
		}
		anchor := now
		if body.AnchorMs != nil {
			anchor = time.UnixMilli(*body.AnchorMs)
		}
		ms := anchor.UnixMilli()
		next := ms - (ms % body.EveryMs) + body.EveryMs
		if next <= now.UnixMilli() {
			next = now.UnixMilli() + body.EveryMs
		}
		return time.UnixMilli(next), nil
	case "cron":
		expr, err := ParseCronExpr(body.Expr)
		if err != nil {
			return time.Time{}, fmt.Errorf("cron: %w", err)
		}
		loc := time.Local
		if body.TZ != "" {
			if l, err := time.LoadLocation(body.TZ); err == nil {
				loc = l
			}
		}
		return expr.Next(now, loc)
	default:
		return time.Time{}, fmt.Errorf("unknown schedule kind %q", body.Kind)
	}
}

func sessionIDFor(scheduleID string) string {
	return "schedule-" + scheduleID
}

func (e *Engine) emitFired(scheduleID, runID string, triggeredAt int64) {
	if e.bcast == nil {
		return
	}
	e.bcast.Broadcast("agent.event", map[string]any{
		"type":        "ScheduleFired",
		"scheduleId":  scheduleID,
		"runId":       runID,
		"triggeredAt": triggeredAt,
	})
}

func (e *Engine) emitRunsChanged(scheduleID, runID string) {
	if e.bcast == nil {
		return
	}
	e.bcast.Broadcast("agent.event", map[string]any{
		"type":       "ScheduleRunsChanged",
		"scheduleId": scheduleID,
		"runId":      runID,
	})
}

var counter int64

func newScheduleID() string {
	counter++
	return fmt.Sprintf("schr-%d-%d", time.Now().UnixNano(), counter)
}