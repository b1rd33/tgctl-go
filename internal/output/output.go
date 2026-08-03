package output

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type EmitOptions struct {
	JSON           bool
	Stdout         io.Writer
	Stderr         io.Writer
	HumanFormatter func(any)
}

func Emit(envelope Envelope, opts EmitOptions) ExitCode {
	stdout := opts.Stdout
	stderr := opts.Stderr
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	if opts.JSON {
		encoded, err := json.Marshal(envelope)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "ERROR [GENERIC]: %v\n", err)
			return Generic
		}
		_, _ = fmt.Fprintln(stdout, string(encoded))
	} else if envelope.OK {
		if opts.HumanFormatter != nil {
			opts.HumanFormatter(envelope.Data)
		} else {
			encoded, _ := json.MarshalIndent(envelope.Data, "", "  ")
			_, _ = fmt.Fprintln(stdout, string(encoded))
		}
	} else {
		_, _ = fmt.Fprintf(stderr, "ERROR [%s]: %s\n", envelope.Error.Code, envelope.Error.Message)
	}

	if envelope.OK {
		return OK
	}
	return ExitCodeFromString(envelope.Error.Code)
}

func ExitCodeFromString(name string) ExitCode {
	switch name {
	case "OK":
		return OK
	case "BAD_ARGS":
		return BadArgs
	case "NOT_AUTHED":
		return NotAuthed
	case "NOT_FOUND":
		return NotFound
	case "FLOOD_WAIT":
		return FloodWait
	case "WRITE_DISALLOWED":
		return WriteDisallowed
	case "NEEDS_CONFIRM":
		return NeedsConfirm
	case "LOCAL_RATE_LIMIT":
		return LocalRateLimit
	case "PREMIUM_REQUIRED":
		return PremiumRequired
	case "PERMISSION_DENIED":
		return PermissionDenied
	case "ARCHIVE_MISSING":
		return ArchiveMissing
	case "ARCHIVE_CHANGED":
		return ArchiveChanged
	case "ARCHIVE_EXTRA":
		return ArchiveExtra
	default:
		return Generic
	}
}

func NewRequestID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("req-%x", b)
}

type ExitCode int

const (
	OK ExitCode = iota
	Generic
	BadArgs
	NotAuthed
	NotFound
	FloodWait
	WriteDisallowed
	NeedsConfirm
	LocalRateLimit
	PremiumRequired
	PermissionDenied
	ArchiveMissing
	ArchiveChanged
	ArchiveExtra
)

func (c ExitCode) String() string {
	switch c {
	case OK:
		return "OK"
	case Generic:
		return "GENERIC"
	case BadArgs:
		return "BAD_ARGS"
	case NotAuthed:
		return "NOT_AUTHED"
	case NotFound:
		return "NOT_FOUND"
	case FloodWait:
		return "FLOOD_WAIT"
	case WriteDisallowed:
		return "WRITE_DISALLOWED"
	case NeedsConfirm:
		return "NEEDS_CONFIRM"
	case LocalRateLimit:
		return "LOCAL_RATE_LIMIT"
	case PremiumRequired:
		return "PREMIUM_REQUIRED"
	case PermissionDenied:
		return "PERMISSION_DENIED"
	case ArchiveMissing:
		return "ARCHIVE_MISSING"
	case ArchiveChanged:
		return "ARCHIVE_CHANGED"
	case ArchiveExtra:
		return "ARCHIVE_EXTRA"
	default:
		return "GENERIC"
	}
}

type Envelope struct {
	OK        bool
	Command   string
	RequestID string
	Data      any
	Warnings  []string
	Error     *ErrorBody
}

type ErrorBody struct {
	Code    string
	Message string
	Extra   map[string]any
}

func Success(command string, data any, requestID string, warnings []string) Envelope {
	w := []string{}
	if warnings != nil {
		w = append(w, warnings...)
	}
	return Envelope{
		OK:        true,
		Command:   command,
		RequestID: requestID,
		Data:      data,
		Warnings:  w,
	}
}

func Fail(command string, code ExitCode, message string, requestID string, extra map[string]any) Envelope {
	if extra == nil {
		extra = map[string]any{}
	}
	return Envelope{
		OK:        false,
		Command:   command,
		RequestID: requestID,
		Error: &ErrorBody{
			Code:    code.String(),
			Message: message,
			Extra:   extra,
		},
	}
}

// errExtraOrder lists the fixed key emission order for known extras. Keys not
// listed here are appended in deterministic (sorted) order so output is
// reproducible across runs.
var errExtraOrder = []string{"retry_after_seconds", "telegram_error", "candidates"}

func (e *ErrorBody) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	writeKV := func(prefix string, key string, val any) error {
		buf.WriteString(prefix)
		kb, _ := json.Marshal(key)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(vb)
		return nil
	}

	if err := writeKV("", "code", e.Code); err != nil {
		return nil, err
	}
	if err := writeKV(",", "message", e.Message); err != nil {
		return nil, err
	}
	seen := map[string]bool{"code": true, "message": true}
	for _, k := range errExtraOrder {
		if v, ok := e.Extra[k]; ok {
			if err := writeKV(",", k, v); err != nil {
				return nil, err
			}
			seen[k] = true
		}
	}
	// Stable order for any remaining keys.
	remaining := make([]string, 0, len(e.Extra))
	for k := range e.Extra {
		if !seen[k] {
			remaining = append(remaining, k)
		}
	}
	sortStrings(remaining)
	for _, k := range remaining {
		if err := writeKV(",", k, e.Extra[k]); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (env Envelope) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	writeKV := func(prefix string, key string, val any) error {
		buf.WriteString(prefix)
		kb, _ := json.Marshal(key)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(vb)
		return nil
	}

	if err := writeKV("", "ok", env.OK); err != nil {
		return nil, err
	}
	if err := writeKV(",", "command", env.Command); err != nil {
		return nil, err
	}
	if err := writeKV(",", "request_id", env.RequestID); err != nil {
		return nil, err
	}
	if env.OK {
		if env.Data != nil {
			if err := writeKV(",", "data", env.Data); err != nil {
				return nil, err
			}
		}
		w := env.Warnings
		if w == nil {
			w = []string{}
		}
		if err := writeKV(",", "warnings", w); err != nil {
			return nil, err
		}
	} else if env.Error != nil {
		if err := writeKV(",", "error", env.Error); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
