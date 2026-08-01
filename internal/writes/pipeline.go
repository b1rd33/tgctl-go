// Package writes implements the shared safety pipeline every Telegram-side
// write command goes through, mirroring the Python flow in
// tgcli.commands.messages._run_write_command and the gates in tgcli.safety.
//
// The ordinary-write pipeline order is fixed (changing it changes the contract):
//  1. Write gate         (--allow-write or TG_ALLOW_WRITE=1, --read-only blocks even with allow)
//  2. Idempotency lookup (cached envelope replay short-circuits before resolve)
//  3. Fuzzy gate         (--fuzzy required for non-int / non-@username selectors)
//  4. Resolver           (DB-only int / @username / fuzzy title)
//  5. Dry-run            (returns payload preview before any client call or rate limiter)
//  6. Local rate limiter (sliding 20 / 60s)
//  7. Audit pre          (NDJSON line, shared request_id)
//  8. Telegram call
//  9. Idempotency record (so a replay returns the same envelope data)
//
// Typed-confirmation commands resolve once in read-only preflight and provide a
// ConfirmedTarget. For those commands steps 3-4 consume that immutable snapshot
// instead of consulting RawSelector or the cache again.
package writes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/b1rd33/tgctl-go/internal/audit"
	"github.com/b1rd33/tgctl-go/internal/dispatch"
	"github.com/b1rd33/tgctl-go/internal/idempotency"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
)

// Args is the flag bundle the pipeline reads.
type Args struct {
	safety.Args
	DryRun                 bool
	IdempotencyKey         string
	IdempotencyFingerprint string
}

// ConfirmedTarget is the immutable peer and typed-confirmation target captured
// by command preflight. When present, Run must not resolve RawSelector again.
type ConfirmedTarget struct {
	ChatID            int64
	ChatTitle         string
	ConfirmationSlot  string
	ConfirmationValue string
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
	// ConfirmedTarget carries the peer resolved and confirmed before writable
	// dispatch. It prevents account/selector TOCTOU changes during execution.
	ConfirmedTarget *ConfirmedTarget
	// Run is the Telegram-side action. It receives the resolved chat id/title
	// and returns the success-envelope `data` map.
	Run func(ctx context.Context, chatID int64, chatTitle string) (map[string]any, error)
	// CommittedExtras carries safe recovery metadata for post-Telegram
	// finalization failures (currently album idempotency recording).
	CommittedExtras map[string]any
	// DurableAudit makes the pre-audit append a required precondition for the
	// Telegram call. Non-durable commands retain best-effort audit behavior.
	DurableAudit bool
}

// Run executes the full pipeline. It returns the runner's `data` map (or an
// error mapped to the standard exit codes by dispatch). Callers wire this
// inside dispatch.Run so audit/envelope/exit code handling is uniform.
func Run(ctx context.Context, db *sql.DB, in PipelineInput) (any, error) {
	// 1. Write gate
	if err := safety.RequireWriteAllowed(in.Args.Args); err != nil {
		return nil, err
	}

	requestID := dispatch.RequestIDFrom(ctx)
	albumReservation := false
	releaseAlbumReservation := func() {
		if albumReservation {
			_ = idempotency.Release(db, in.Args.IdempotencyKey, in.Cmd, requestID)
			albumReservation = false
		}
	}
	defer releaseAlbumReservation()
	replay := func(cached map[string]any) (any, error) {
		if expected := strings.TrimSpace(in.Args.IdempotencyFingerprint); expected != "" {
			actual := strings.TrimSpace(fmt.Sprintf("%v", cached["idempotency_fingerprint"]))
			if actual == "" || actual != expected {
				return nil, safety.NewBadArgs("Idempotency key %q was already used for a different album request", in.Args.IdempotencyKey)
			}
		}
		if err := validateConfirmedReplay(in.Args.IdempotencyKey, cached, in.ConfirmedTarget); err != nil {
			return nil, err
		}
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

	// 2. Idempotency lookup (after write gate, before resolve, matching Python).
	// A dry-run is always a fresh plan: it must not replay or write durable
	// idempotency state, even when the caller supplied a key.
	if !in.Args.DryRun {
		if strings.TrimSpace(in.Args.IdempotencyFingerprint) != "" && in.Args.IdempotencyKey != "" {
			cached, reserved, err := idempotency.Reserve(db, in.Args.IdempotencyKey, in.Cmd, requestID, strings.TrimSpace(in.Args.IdempotencyFingerprint))
			if err != nil {
				return nil, err
			}
			if !reserved {
				if idempotency.IsPending(cached) {
					return nil, safety.NewBadArgs("Idempotency key %q is already in progress", in.Args.IdempotencyKey)
				}
				return replay(cached)
			}
			albumReservation = true
		} else if cached, err := idempotency.Lookup(db, in.Args.IdempotencyKey, in.Cmd); err != nil {
			return nil, err
		} else if cached != nil {
			return replay(cached)
		}
	}

	var chatID int64
	var chatTitle string
	if in.ConfirmedTarget != nil {
		chatID = in.ConfirmedTarget.ChatID
		chatTitle = in.ConfirmedTarget.ChatTitle
	} else {
		// 3. Fuzzy gate
		if err := safety.RequireExplicitOrFuzzy(in.Args.Args, in.RawSelector); err != nil {
			return nil, err
		}

		// 4. Resolver (DB-only)
		var err error
		chatID, chatTitle, err = resolve.ResolveChatDB(db, in.RawSelector)
		if err != nil {
			return nil, err
		}
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
		releaseAlbumReservation()
		return nil, err
	}

	// 7. Audit pre — NDJSON line sharing the dispatch request_id.
	if in.AuditPath != "" {
		auditErr := audit.Pre(in.AuditPath, audit.PreEntry{
			Cmd:               in.Cmd,
			RequestID:         dispatch.RequestIDFrom(ctx),
			ResolvedChatID:    chatID,
			ResolvedChatTitle: chatTitle,
			TelethonMethod:    in.TelethonMethod,
			PayloadPreview:    in.PayloadPreview,
			DryRun:            false,
		})
		if auditErr != nil && (in.DurableAudit || strings.TrimSpace(in.Args.IdempotencyFingerprint) != "") {
			releaseAlbumReservation()
			return nil, errors.New("durable audit preflight failed")
		}
	}

	// 8. Telegram call
	data, err := in.Run(ctx, chatID, chatTitle)
	if err != nil {
		var committed *safety.CommittedWrite
		if errors.As(err, &committed) {
			albumReservation = false
		} else {
			releaseAlbumReservation()
		}
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
		if fingerprint := strings.TrimSpace(in.Args.IdempotencyFingerprint); fingerprint != "" {
			envelope["idempotency_fingerprint"] = fingerprint
		}
		if in.ConfirmedTarget != nil {
			envelope["confirmed_target"] = map[string]any{
				"chat_id": strconv.FormatInt(in.ConfirmedTarget.ChatID, 10),
				"slot":    in.ConfirmedTarget.ConfirmationSlot,
				"value":   in.ConfirmedTarget.ConfirmationValue,
			}
		}
		var recordErr error
		if albumReservation {
			recordErr = idempotency.Finalize(db, in.Args.IdempotencyKey, in.Cmd, requestID, envelope)
		} else {
			recordErr = idempotency.Record(db, in.Args.IdempotencyKey, in.Cmd, requestID, envelope)
		}
		if recordErr != nil {
			if strings.TrimSpace(in.Args.IdempotencyFingerprint) != "" {
				albumReservation = false
				return nil, safety.NewCommittedWriteWithExtras("album committed but idempotency finalization failed; do not retry blindly", errors.New("idempotency cache finalization failed"), in.CommittedExtras)
			}
			return nil, recordErr
		}
		albumReservation = false
	}
	return data, nil
}

func validateConfirmedReplay(key string, cached map[string]any, target *ConfirmedTarget) error {
	if target == nil {
		return nil
	}
	if metadata, ok := cached["confirmed_target"].(map[string]any); ok {
		slot := strings.TrimSpace(fmt.Sprintf("%v", metadata["slot"]))
		value := normalizedValue(metadata["value"])
		if slot != target.ConfirmationSlot || value != target.ConfirmationValue {
			return safety.NewBadArgs("Idempotency key %q was already used for a different confirmed target", key)
		}
		if chatID, exists := metadata["chat_id"]; exists {
			if normalizedValue(chatID) == strconv.FormatInt(target.ChatID, 10) {
				return nil
			}
			return safety.NewBadArgs("Idempotency key %q was already used for a different confirmed target", key)
		}
	}

	data, ok := cached["data"].(map[string]any)
	if !ok {
		return safety.NewBadArgs("Idempotency key %q has no confirmed target metadata", key)
	}
	chat, ok := data["chat"].(map[string]any)
	if !ok {
		return safety.NewBadArgs("Idempotency key %q has no confirmed target metadata", key)
	}
	if normalizedValue(chat["chat_id"]) != strconv.FormatInt(target.ChatID, 10) {
		return safety.NewBadArgs("Idempotency key %q was already used for a different confirmed target", key)
	}
	value := data[target.ConfirmationSlot]
	if target.ConfirmationSlot == "chat_id" {
		value = chat["chat_id"]
	}
	if normalizedValue(value) != target.ConfirmationValue {
		return safety.NewBadArgs("Idempotency key %q was already used for a different confirmed target", key)
	}
	return nil
}

func normalizedValue(value any) string {
	switch v := value.(type) {
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", value))
	}
}
