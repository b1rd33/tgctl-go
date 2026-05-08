package output

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
