package client

import (
	"context"
	"errors"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

func (g *GotdClient) Logout(ctx context.Context) error {
	if g.sessionStorage == nil {
		return safety.NewBadArgs("logout requires writable account session storage")
	}
	// A successful logout can end the transport before this method returns.
	// Keep session ownership until cleanup finishes so a concurrent login's
	// new credential cannot be deleted by this operation.
	g.sessionStorage.logoutMu.Lock()
	defer g.sessionStorage.logoutMu.Unlock()
	if _, err := g.api.AuthLogOut(ctx); err != nil {
		return mapRPCErr(err)
	}
	if err := g.sessionStorage.Clear(); err != nil {
		return safety.NewCommittedWriteWithExtras("Telegram logout confirmed but local session removal failed", errors.New("local session removal failed"), map[string]any{"logged_out": true, "session_removed": false})
	}
	return nil
}
