package ctxengine

import "context"

// AfterTurn is currently a no-op. Reserved for cross-turn side effects
// (memory write-back, summary refresh).
func (a *DefaultAssembler) AfterTurn(ctx context.Context, p AfterTurnParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
