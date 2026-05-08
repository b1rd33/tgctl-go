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
