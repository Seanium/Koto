package engine

// TargetChangedError: the target file is not as planned; nothing was written.
type TargetChangedError struct{ Msg string }

func (e *TargetChangedError) Error() string { return e.Msg }

// CodedError carries a code the app maps to a status (HUB_BUSY, SESSION_OPEN, AMBIGUOUS).
type CodedError struct {
	Code string
	Msg  string
}

func (e *CodedError) Error() string { return e.Msg }
