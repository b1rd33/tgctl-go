// Package writes implements the shared safety pipeline every Telegram-side
// write command goes through, mirroring the Python flow in
// tgcli.commands.messages._run_write_command and the gates in tgcli.safety.
//
// The pipeline order is fixed (changing it changes the contract):
//  1. Write gate         (--allow-write or TG_ALLOW_WRITE=1, --read-only blocks even with allow)
//  2. Idempotency lookup (cached envelope replay short-circuits before resolve)
//  3. Fuzzy gate         (--fuzzy required for non-int / non-@username selectors)
//  4. Resolver           (DB-only int / @username / fuzzy title)
//  5. Dry-run            (returns payload preview before any client call or rate limiter)
//  6. Local rate limiter (sliding 20 / 60s)
//  7. Audit pre          (NDJSON line, shared request_id)
//  8. Telegram call
//  9. Idempotency record (so a replay returns the same envelope data)
package writes

import (
	"context"
	"database/sql"

	"github.com/b1rd33/tgctl-go/internal/audit"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/idempotency"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

// Args is the flag bundle the pipeline reads.
type Args struct {
	safety.Args
	DryRun         bool
	IdempotencyKey string
}

// PipelineInput is what the caller hands the pipeline.
type PipelineInput struct {
	Cmd            string
	RawSelector    string
	Args           Args
	DBPath         string
	AuditPath      string
	TelethonMethod string
	// PayloadPreview is what the dry-run envelope returns and what shows up in audit_pre.
	PayloadPreview map[string]any
	// Run is the Telegram-side action. It receives the resolved chat id/title
	// and returns the success-envelope `data` map.
	Run func(ctx context.Context, chatID int64, chatTitle string) (map[string]any, error)
}

// Run executes the full pipeline. It returns the runner's `data` map (or an
// error mapped to the standard exit codes by dispatch). Callers wire this
// inside dispatch.Run so audit/envelope/exit code handling is uniform.
func Run(ctx context.Context, db *sql.DB, in PipelineInput) (any, error) {
	// 1. Write gate
	if err := safety.RequireWriteAllowed(in.Args.Args); err != nil {
		return nil, err
	}

	// 2. Idempotency lookup (after write gate, before resolve, matching Python).
	if cached, err := idempotency.Lookup(db, in.Args.IdempotencyKey, in.Cmd); err != nil {
		return nil, err
	} else if cached != nil {
		// The envelope's data with `idempotent_replay: true` flag.
		if data, ok := cached["data"].(map[string]any); ok {
			out := map[string]any{}
			for k, v := range data {
				out[k] = v
			}
			out["idempotent_replay"] = true
			return out, nil
		}
		// Defensive: if the cached envelope shape was unexpected, still return it.
		return cached, nil
	}

	// 3. Fuzzy gate
	if err := safety.RequireExplicitOrFuzzy(in.Args.Args, in.RawSelector); err != nil {
		return nil, err
	}

	// 4. Resolver (DB-only)
	chatID, chatTitle, err := resolve.ResolveChatDB(db, in.RawSelector)
	if err != nil {
		return nil, err
	}

	// 5. Dry-run short-circuits BEFORE any Telegram contact.
	if in.Args.DryRun {
		out := map[string]any{
			"dry_run": true,
			"chat":    map[string]any{"chat_id": chatID, "title": chatTitle},
		}
		for k, v := range in.PayloadPreview {
			out[k] = v
		}
		return out, nil
	}

	// 6. Local rate limiter
	if err := safety.OutboundWriteLimiter.CheckOrError(); err != nil {
		return nil, err
	}

	// 7. Audit pre — NDJSON line sharing the dispatch request_id.
	if in.AuditPath != "" {
		_ = audit.Pre(in.AuditPath, audit.PreEntry{
			Cmd:               in.Cmd,
			RequestID:         dispatch.RequestIDFrom(ctx),
			ResolvedChatID:    chatID,
			ResolvedChatTitle: chatTitle,
			TelethonMethod:    in.TelethonMethod,
			PayloadPreview:    in.PayloadPreview,
			DryRun:            false,
		})
	}

	// 8. Telegram call
	data, err := in.Run(ctx, chatID, chatTitle)
	if err != nil {
		return nil, err
	}

	// Always include the resolved chat in the data so envelopes are uniform.
	if _, ok := data["chat"]; !ok {
		data["chat"] = map[string]any{"chat_id": chatID, "title": chatTitle}
	}

	// 9. Idempotency record. Stored shape is the full envelope so a future
	// lookup can return identical data with `idempotent_replay:true` added.
	if in.Args.IdempotencyKey != "" {
		envelope := map[string]any{
			"ok":         true,
			"command":    in.Cmd,
			"request_id": dispatch.RequestIDFrom(ctx),
			"data":       data,
			"warnings":   []string{},
		}
		if err := idempotency.Record(db, in.Args.IdempotencyKey, in.Cmd, dispatch.RequestIDFrom(ctx), envelope); err != nil {
			return nil, err
		}
	}
	return data, nil
}
