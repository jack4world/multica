// Package remediate decides whether one write to a remediation item is allowed.
//
// Same shape and same reasons as internal/auditgate: a pure function over
// values, no database, no request, no transaction. 后续审计 is the part of an
// internal audit that outlives the engagement, and the rules about who may say
// a problem is fixed are exactly the rules nobody should have to reconstruct
// from four handlers.
//
// WHY A SECOND PACKAGE. The review chain and the remediation chain share no
// rule. The review chain asks about ranks on an engagement and a preparer; this
// one asks about the person who owes the fix and the auditor who checks it.
// Folding them together would produce a decision function whose every branch
// began by asking which chain it was in.
package remediate

import (
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/auditmode"
)

// Event names what the trail records. Refusals have no event: the trail says
// what happened, not what was attempted.
type Event string

const (
	EventStarted   Event = "remediation_started"
	EventSubmitted Event = "remediation_submitted"
	EventVerified  Event = "remediation_verified"
	EventRejected  Event = "remediation_rejected"
	EventCancelled Event = "remediation_cancelled"
)

// DenyCode classifies a refusal so a transport can map it to a status and a
// client can render it in the reader's language.
type DenyCode string

const (
	DenyIllegalTransition DenyCode = "illegal_transition"
	DenyVerifierRequired  DenyCode = "verifier_required"
	DenySelfVerification  DenyCode = "self_verification"
	DenyNotResponsible    DenyCode = "not_responsible"
	DenyClosed            DenyCode = "remediation_closed"
	DenyAgent             DenyCode = "agent_not_permitted"
	DenyLeavesChain       DenyCode = "leaves_chain"
	DenyNoteRequired      DenyCode = "note_required"
	DenyNotRemediation    DenyCode = "not_a_remediation_item"
)

// Input is everything the decision depends on. All values, all supplied.
type Input struct {
	// IsRemediation reports that this issue is on the ledger — it has a
	// remediation record naming its department and the engagement that raised
	// it. An ordinary issue is not allowed into the chain: the chain's rules
	// all read facts only a ledger row carries.
	IsRemediation bool
	// From is the current status key; empty means the issue is being created.
	From string
	// To is the target status key, or empty when the write leaves status alone.
	To string
	// ActorUserID identifies the acting person; empty for an agent.
	//
	// A USER id, not a member id, and the name says so because getting it
	// wrong is silent: `issue.assignee_id` holds a user id for a member
	// assignee (see validateAssigneePair), so comparing it against a member id
	// never matches, and the self-verification rule below would then let the
	// person who owes the fix close their own item.
	ActorUserID string
	// ResponsibleUserID is the person who owes the fix — the item's assignee —
	// or empty when nobody is assigned yet. Same namespace as ActorUserID.
	ResponsibleUserID string
	// ActorIsVerifier reports that the actor holds a reviewer rank on the
	// engagement that RAISED this item. Any rank qualifies: verifying a fix is
	// the audit function's act, and which level performs it is not a control.
	ActorIsVerifier bool
	// ActorIsAdmin is true for a workspace owner or admin.
	ActorIsAdmin bool
	// ActorIsAgent is true when the write comes from an agent.
	ActorIsAgent bool
	// Note is the free text accompanying the write: what was fixed, what was
	// checked, or why the fix does not stand.
	Note string
	// RefusedActor marks a caller with no standing in the auditee at all.
	RefusedActor bool
}

// Decision is the answer.
type Decision struct {
	Allowed bool
	Code    DenyCode
	Reason  string
	// Event is what the trail should record, or empty when this write is not a
	// transition worth recording.
	Event Event
}

func allow() Decision { return Decision{Allowed: true} }

func record(event Event) Decision { return Decision{Allowed: true, Event: event} }

func deny(code DenyCode, format string, args ...any) Decision {
	return Decision{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// Governs reports whether a transition between these two statuses could be
// governed at all, from status keys alone. Callers short-circuit on it so the
// overwhelmingly common issue write costs nothing.
func Governs(from, to string) bool {
	return isRemediation(from) || isRemediation(to)
}

func isRemediation(status string) bool {
	_, ok := auditmode.RemediationStatusByKey(status)
	return ok
}

// Decide answers one write to a remediation item.
func Decide(in Input) Decision {
	if in.RefusedActor {
		return deny(DenyVerifierRequired,
			"you are not a member of this auditee and cannot move its remediation items")
	}

	fromChain := in.From != "" && isRemediation(in.From)
	toChain := isRemediation(in.To)
	if !fromChain && !toChain {
		return allow()
	}

	// The ledger's rules read the department that owes the fix and the
	// engagement that raised it. An ordinary issue has neither, so putting one
	// on a remediation status would produce an item nobody owns that nobody is
	// qualified to close — which is the exact failure the ledger exists to
	// prevent.
	if !in.IsRemediation {
		return deny(DenyNotRemediation,
			"%q is a remediation status; raise the problem as a remediation item from the engagement that found it", in.To)
	}

	// A closed item is immutable, checked before everything else so no rank and
	// no admin reads as an exception to it.
	if in.From == auditmode.StatusRemediationClosed {
		return deny(DenyClosed,
			"this remediation item is closed; a problem that recurs is a new item, not an edited closure")
	}

	// No status change, or the status it already has: this write edits content.
	// The second case matters because the batch endpoint is all-or-nothing.
	if in.To == "" || in.To == in.From {
		return allow()
	}

	if in.To == "cancelled" {
		if in.ActorIsAgent {
			return deny(DenyAgent, "an agent cannot cancel a remediation item")
		}
		if !in.ActorIsVerifier && !in.ActorIsAdmin {
			return deny(DenyVerifierRequired,
				"cancelling a remediation item needs a reviewer role on the engagement that raised it, or workspace admin")
		}
		// The same hole as a preparer cancelling their own workpaper: an
		// inconvenient item would be disposed of by the one party the ledger
		// exists to hold to a deadline.
		if in.ResponsibleUserID != "" && in.ActorUserID == in.ResponsibleUserID {
			return deny(DenySelfVerification,
				"you are responsible for this item and cannot cancel it; ask the audit function")
		}
		return record(EventCancelled)
	}

	// Leaving the chain for an ordinary status. This is the escape hatch that
	// matters most: 待验证 -> done would close an item with nobody verifying
	// anything, which is the whole failure the chain prevents.
	if fromChain && !toChain {
		return deny(DenyLeavesChain,
			"a remediation item cannot move from %q to %q; it stays on the ledger until it is verified or cancelled",
			in.From, in.To)
	}

	switch in.To {
	case auditmode.StatusRemediating:
		// Two ways in: starting work, and a verifier sending a fix back.
		if in.From == auditmode.StatusPendingVerification {
			return decideVerification(in)
		}
		if in.ActorIsAgent {
			return deny(DenyAgent, "an agent cannot start remediation work")
		}
		if !responsibleOrAudit(in) {
			return deny(DenyNotResponsible,
				"starting remediation is for the person responsible for it, or the audit function")
		}
		return record(EventStarted)

	case auditmode.StatusPendingVerification:
		if in.From != auditmode.StatusRemediating {
			return deny(DenyIllegalTransition,
				"an item is submitted for verification from %q, not from %q",
				auditmode.StatusRemediating, in.From)
		}
		if in.ActorIsAgent {
			// Submitting is a claim that the problem is fixed. An agent can
			// gather the evidence for that claim; it cannot make it.
			return deny(DenyAgent, "an agent cannot submit a remediation item for verification")
		}
		if !responsibleOrAudit(in) {
			return deny(DenyNotResponsible,
				"submitting for verification is for the person responsible for the fix")
		}
		if strings.TrimSpace(in.Note) == "" {
			return deny(DenyNoteRequired,
				"say what was done before submitting for verification; the verifier has no other account of it")
		}
		return record(EventSubmitted)

	case auditmode.StatusRemediationClosed:
		if in.From != auditmode.StatusPendingVerification {
			return deny(DenyIllegalTransition,
				"an item is closed from %q, not from %q; closure is a verification, not a status anyone can set",
				auditmode.StatusPendingVerification, in.From)
		}
		return decideVerification(in)
	}

	return deny(DenyIllegalTransition, "%q is not a status a remediation item can move to from %q", in.To, in.From)
}

// responsibleOrAudit reports whether the actor is the person who owes the fix,
// or the audit function acting on their behalf. The audit function is included
// because an item is often started in a meeting, by the auditor holding the
// ledger, before the responsible person has opened the system at all.
func responsibleOrAudit(in Input) bool {
	if in.ActorIsVerifier || in.ActorIsAdmin {
		return true
	}
	if in.ResponsibleUserID == "" {
		// Nobody is assigned yet, so nobody is the responsible person. Someone
		// from the audit function has to assign it first — an item that starts
		// moving with no owner is the item that later has no owner to chase.
		return false
	}
	return in.ActorUserID == in.ResponsibleUserID
}

// decideVerification authorizes a verifier's ruling — closing an item or
// sending it back. Both need the same standing and both are closed to the
// person who did the work, which is what the ledger exists to guarantee.
func decideVerification(in Input) Decision {
	if in.ActorIsAgent {
		return deny(DenyAgent, "an agent cannot verify a remediation item")
	}
	if !in.ActorIsVerifier && !in.ActorIsAdmin {
		return deny(DenyVerifierRequired,
			"verifying a remediation item needs a reviewer role on the engagement that raised it, or workspace admin")
	}
	if in.ResponsibleUserID != "" && in.ActorUserID == in.ResponsibleUserID {
		return deny(DenySelfVerification,
			"you are responsible for this item and cannot verify your own fix; ask the audit function")
	}
	if strings.TrimSpace(in.Note) == "" {
		if in.To == auditmode.StatusRemediating {
			return deny(DenyNoteRequired,
				"say why the fix does not stand; the responsible person has no other way to know what is still missing")
		}
		// Closure with no account of what was checked is a timestamp pretending
		// to be a control. 后续审计 asks what evidence closed the item, and a
		// blank field answers nobody.
		return deny(DenyNoteRequired,
			"say what you checked before closing; a closure with no account of it proves nothing later")
	}
	if in.To == auditmode.StatusRemediating {
		return record(EventRejected)
	}
	return record(EventVerified)
}
