//go:build !integration && !e2e

package wagering

import "testing"

func TestKinds(t *testing.T) {
	for _, k := range []Kind{KindBet, KindWin, KindLoss, KindRefund, KindRollback} {
		if !k.IsValid() || !k.IsExternal() {
			t.Errorf("%s must be a valid external kind", k)
		}
	}
	if !KindOpening.IsValid() || KindOpening.IsExternal() {
		t.Error("OPENING is valid but internal only")
	}
	if Kind("JACKPOT").IsValid() {
		t.Error("unknown kind must be invalid")
	}
	if !KindRefund.IsReversal() || !KindRollback.IsReversal() || KindWin.IsReversal() {
		t.Error("only REFUND and ROLLBACK are reversals")
	}
}

func TestStatusTransitions(t *testing.T) {
	allowed := map[Status][]Status{
		StatusPending:          {StatusProcessed, StatusRejected, StatusPendingReference, StatusFailed},
		StatusPendingReference: {StatusProcessed, StatusRejected, StatusFailed},
	}
	all := []Status{StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed}
	for _, from := range all {
		for _, to := range all {
			want := false
			for _, a := range allowed[from] {
				if a == to {
					want = true
				}
			}
			if got := from.CanTransitionTo(to); got != want {
				t.Errorf("%s -> %s: got %v want %v", from, to, got, want)
			}
		}
	}
	for _, s := range []Status{StatusProcessed, StatusRejected, StatusFailed} {
		if !s.IsTerminal() {
			t.Errorf("%s must be terminal", s)
		}
	}
	for _, s := range []Status{StatusPending, StatusPendingReference} {
		if s.IsTerminal() {
			t.Errorf("%s must not be terminal", s)
		}
	}
	if Status("DONE").IsValid() {
		t.Error("unknown status must be invalid")
	}
}
