package ctxengine

import "context"

// Bootstrap is currently a no-op. The method exists so the agent loop can
// call it at startup (e.g. to open a SQLite-backed projection registry)
// without changing the ContextEngine interface in a future spec.
func (a *DefaultAssembler) Bootstrap(ctx context.Context, p BootstrapParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// Maintain is currently a no-op. Reserved for Dreaming / Cron specs that
// run periodic housekeeping on the context engine's internal state.
func (a *DefaultAssembler) Maintain(ctx context.Context, p MaintainParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// Dispose releases any internal resources. v0 has nothing to release; the
// method exists so the agent loop can call it on shutdown.
func (a *DefaultAssembler) Dispose(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
