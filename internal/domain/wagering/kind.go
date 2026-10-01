package wagering

// Kind is the operation type.
type Kind string

// Kinds. OPENING is internal only.
const (
	KindOpening  Kind = "OPENING"
	KindBet      Kind = "BET"
	KindWin      Kind = "WIN"
	KindLoss     Kind = "LOSS"
	KindRefund   Kind = "REFUND"
	KindRollback Kind = "ROLLBACK"
)

// IsValid reports whether k is a known kind.
func (k Kind) IsValid() bool { return k == KindOpening || k.IsExternal() }

// IsExternal reports whether k may arrive over HTTP or SQS.
func (k Kind) IsExternal() bool {
	switch k {
	case KindBet, KindWin, KindLoss, KindRefund, KindRollback:
		return true
	default:
		return false
	}
}

// IsReversal reports whether k reverses another transaction.
func (k Kind) IsReversal() bool { return k == KindRefund || k == KindRollback }

// Status is the processing state of a transaction.
type Status string

// Statuses. PROCESSED, REJECTED and FAILED are terminal.
const (
	StatusPending          Status = "PENDING"
	StatusPendingReference Status = "PENDING_REFERENCE"
	StatusProcessed        Status = "PROCESSED"
	StatusRejected         Status = "REJECTED"
	StatusFailed           Status = "FAILED"
)

var transitions = map[Status][]Status{
	StatusPending:          {StatusProcessed, StatusRejected, StatusPendingReference, StatusFailed},
	StatusPendingReference: {StatusProcessed, StatusRejected, StatusFailed},
}

// IsValid reports whether s is a known status.
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether s accepts no further transitions.
func (s Status) IsTerminal() bool {
	return s == StatusProcessed || s == StatusRejected || s == StatusFailed
}

// CanTransitionTo reports whether the state machine allows s → next.
func (s Status) CanTransitionTo(next Status) bool {
	for _, allowed := range transitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// Origin distinguishes the internal OPENING from external operations.
type Origin string

// Origins.
const (
	OriginInternal Origin = "INTERNAL"
	OriginExternal Origin = "EXTERNAL"
)
