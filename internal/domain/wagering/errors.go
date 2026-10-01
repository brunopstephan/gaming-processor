package wagering

import "errors"

var (
	// ErrInvalidTransaction reports invalid construction or rehydration data.
	ErrInvalidTransaction = errors.New("wagering: invalid transaction")
	// ErrInvalidTransition reports a state change the state machine forbids.
	ErrInvalidTransition = errors.New("wagering: invalid state transition")
)
