package remediate

import "github.com/multica-ai/multica/server/internal/auditmode"

// Available answers "what can this person do with this remediation item right
// now".
//
// It asks Decide about each candidate rather than describing the chain again,
// for the reason auditgate.Available does: a client that knows the ledger's
// order is a second copy of the control, and the first time the rules change
// one of the two copies is wrong and nobody finds out until someone is refused
// a button they were shown.
func Available(in Input) []Action {
	candidates := []string{
		auditmode.StatusRemediating,
		auditmode.StatusPendingVerification,
		auditmode.StatusRemediationClosed,
		"cancelled",
	}

	actions := make([]Action, 0, 3)
	for _, to := range candidates {
		if to == in.From {
			continue
		}
		probe := in
		probe.To = to
		// Probed WITH a note, so an action that requires one is offered rather
		// than hidden by the very requirement the button exists to collect.
		probe.Note = noteProbe
		d := Decide(probe)
		if !d.Allowed || d.Event == "" {
			continue
		}
		actions = append(actions, Action{
			Event:        d.Event,
			To:           to,
			RequiresNote: requiresNote(in.From, to),
		})
	}
	return actions
}

// noteProbe stands in for the not-yet-typed note while probing. Any non-blank
// value works; it is never stored.
const noteProbe = "probe"

// Action is one thing the viewer may do, as the interface should present it.
type Action struct {
	Event Event `json:"event"`
	// To is the status to send, so the client acts through the ordinary update
	// endpoint rather than needing a bespoke one per action.
	To string `json:"to"`
	// RequiresNote tells the client to collect one BEFORE sending, so the
	// common case never reaches a refusal.
	RequiresNote bool `json:"requires_note"`
}

// requiresNote mirrors the rule in Decide: submitting, closing and sending back
// all need an account of what happened. Only starting work and cancelling do
// not.
func requiresNote(from, to string) bool {
	switch to {
	case auditmode.StatusPendingVerification:
		return from == auditmode.StatusRemediating
	case auditmode.StatusRemediationClosed:
		return true
	case auditmode.StatusRemediating:
		return from == auditmode.StatusPendingVerification
	}
	return false
}
