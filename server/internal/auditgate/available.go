package auditgate

import "github.com/multica-ai/multica/server/internal/auditmode"

// Available answers "what can this person do with this workpaper right now".
//
// It exists so the INTERFACE never re-implements the chain. A client that knows
// "after 一级复核 comes 二级复核" is a second copy of the control, and two copies
// drift — the first time someone adds a level or changes who may cancel, one of
// them is wrong and nobody finds out until an auditor is refused a button they
// were shown.
//
// So this asks Decide about each candidate target rather than describing the
// chain again. Agreement with enforcement is structural, not maintained: a rule
// added to Decide changes what is offered on the same commit.
func Available(in Input) []Action {
	candidates := []string{
		auditmode.StatusDrafting,
		auditmode.StatusReviewL1,
		auditmode.StatusReviewL2,
		auditmode.StatusReviewL3,
		auditmode.StatusFiled,
		"cancelled",
	}

	actions := make([]Action, 0, 3)
	for _, to := range candidates {
		if to == in.From {
			continue
		}
		probe := in
		probe.To = to
		// Probed WITH a reason, so a rejection is offered rather than hidden by
		// the very requirement the button exists to collect. RequiresReason
		// below is what tells the client to ask for one first.
		probe.Reason = reasonProbe
		d := Decide(probe)
		// Event is what separates a real step in the chain from a write the
		// gate merely tolerates — a content edit is allowed but is not an
		// action anyone should be offered a button for.
		if !d.Allowed || d.Event == "" {
			continue
		}
		actions = append(actions, Action{
			Event:          d.Event,
			To:             to,
			RequiresReason: requiresReason(in.From, to),
		})
	}
	return actions
}

// reasonProbe stands in for the reviewer's not-yet-typed reason while probing.
// Any non-blank value works; it is never stored.
const reasonProbe = "probe"

// Action is one thing the viewer may do, as the interface should present it.
type Action struct {
	// Event is what the trail will record, which is also what the client keys
	// its label off — "passed review" and "filed" are different words for the
	// reader even though both advance the chain.
	Event Event `json:"event"`
	// To is the status to send, so the client can act through the ordinary
	// update endpoint rather than needing a bespoke one per action.
	To string `json:"to"`
	// RequiresReason tells the client to collect one BEFORE sending, so the
	// common case never reaches a refusal.
	RequiresReason bool `json:"requires_reason"`
}

// requiresReason mirrors the rule in Decide: returning a workpaper to its
// preparer needs one, because "rejected" with no reason leaves them told their
// work does not stand without being told what to fix.
func requiresReason(from, to string) bool {
	if to != auditmode.StatusDrafting {
		return false
	}
	_, isReview := levelFor(from)
	return isReview
}
