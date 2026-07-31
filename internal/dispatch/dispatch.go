package dispatch

import (
	"context"
	"errors"

	"github.com/b1rd33/tgctl-go/internal/audit"
	"github.com/b1rd33/tgctl-go/internal/output"
	"github.com/b1rd33/tgctl-go/internal/resolve"
	"github.com/b1rd33/tgctl-go/internal/safety"
	"github.com/b1rd33/tgctl-go/internal/store"
)

// Runner returns the success-envelope `data` payload, or an error.
type Runner func(ctx context.Context) (any, error)

type requestIDKey struct{}

// RequestIDFrom returns the dispatch-issued request id from a context.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey{}).(string); ok {
		return v
	}
	return ""
}

// Options configure how Run emits and audits a single command.
type Options struct {
	JSON           bool
	Stdout         any // io.Writer; left as any to avoid an import in the type
	Stderr         any
	HumanFormatter func(any)
	AuditPath      string
	Args           map[string]any
}

// Classify maps a known error to (exit code, message, envelope-extra fields).
func Classify(err error) (output.ExitCode, string, map[string]any) {
	// A durable write with failed finalization must win over any classified
	// cause it wraps so callers are explicitly warned not to retry blindly.
	var committed *safety.CommittedWrite
	if errors.As(err, &committed) {
		return output.Generic, committed.Error(), map[string]any{
			"committed": true,
			"partial":   true,
		}
	}
	var ambiguous *resolve.Ambiguous
	if errors.As(err, &ambiguous) {
		return output.BadArgs, ambiguous.Error(), map[string]any{"candidates": ambiguous.Candidates}
	}
	var notFound *resolve.NotFound
	if errors.As(err, &notFound) {
		return output.NotFound, notFound.Error(), nil
	}
	var dbMissing *store.DatabaseMissing
	if errors.As(err, &dbMissing) {
		return output.NotFound, dbMissing.Error(), nil
	}
	var badArgs *safety.BadArgs
	if errors.As(err, &badArgs) {
		return output.BadArgs, badArgs.Error(), nil
	}
	var missingCreds *safety.MissingCredentials
	if errors.As(err, &missingCreds) {
		return output.NotAuthed, missingCreds.Error(), nil
	}
	var sessionLocked *safety.SessionLocked
	if errors.As(err, &sessionLocked) {
		return output.Generic, sessionLocked.Error(), nil
	}
	var writeDisallowed *safety.WriteDisallowed
	if errors.As(err, &writeDisallowed) {
		return output.WriteDisallowed, writeDisallowed.Error(), nil
	}
	var needsConfirm *safety.NeedsConfirm
	if errors.As(err, &needsConfirm) {
		return output.NeedsConfirm, needsConfirm.Error(), nil
	}
	var rateLimited *safety.LocalRateLimited
	if errors.As(err, &rateLimited) {
		return output.LocalRateLimit, rateLimited.Error(), map[string]any{
			"retry_after_seconds": rateLimited.RetryAfterSeconds,
		}
	}
	var floodWait *safety.FloodWait
	if errors.As(err, &floodWait) {
		return output.FloodWait, floodWait.Error(), map[string]any{
			"retry_after_seconds": floodWait.Seconds,
		}
	}
	var premium *safety.PremiumRequired
	if errors.As(err, &premium) {
		return output.PremiumRequired, premium.Error(), map[string]any{
			"telegram_error": "PREMIUM_ACCOUNT_REQUIRED",
		}
	}
	return output.Generic, err.Error(), nil
}

// Run executes runner under a dispatch chokepoint: generates a request id,
// classifies known errors, builds the output envelope, audits, and emits.
// Returns the process exit code.
func Run(name string, opts Options, runner Runner) int {
	requestID := output.NewRequestID()
	ctx := context.WithValue(context.Background(), requestIDKey{}, requestID)

	data, err := runner(ctx)

	var envelope output.Envelope
	var auditExtra map[string]any
	var result string

	if err != nil {
		code, msg, extra := Classify(err)
		envelope = output.Fail(name, code, msg, requestID, extra)
		auditExtra = map[string]any{"error_code": code.String()}
		result = "fail"
	} else {
		envelope = output.Success(name, data, requestID, nil)
		result = "ok"
	}

	if opts.AuditPath != "" {
		_ = audit.Write(opts.AuditPath, name, requestID, opts.Args, result, auditExtra)
	}

	emit := output.EmitOptions{
		JSON:           opts.JSON,
		HumanFormatter: opts.HumanFormatter,
	}
	if w, ok := opts.Stdout.(interface{ Write(p []byte) (int, error) }); ok {
		emit.Stdout = w
	}
	if w, ok := opts.Stderr.(interface{ Write(p []byte) (int, error) }); ok {
		emit.Stderr = w
	}
	return int(output.Emit(envelope, emit))
}
