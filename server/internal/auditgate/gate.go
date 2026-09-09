// Package auditgate decides whether one issue status write is allowed inside an
// audit engagement.
//
// It is deliberately a PURE function over values: no database, no request, no
// transaction. The three-level review chain is a control, and a control whose
// rules are spread across handlers is one nobody can read in full. Here the
// whole matrix fits on a screen and is exercised as a table.
//
// The caller supplies the facts; see Input. Callers must short-circuit before
// asking (an issue outside an engagement, or a transition with no audit status
// on either side, cannot be governed) so that the overwhelmingly common issue
// write costs nothing.
package auditgate

import (
	"fmt"

	"github.com/multica-ai/multica/server/internal/auditmode"
)

// Level is a reviewer's rank on one engagement. A member holds at most one,
// which is enforced by a unique constraint on the role table rather than
// checked here — "nobody reviews at two levels" is structural, not a rule that
// can be forgotten.
type Level string

const (
	LevelL1 Level = "reviewer_l1"
	LevelL2 Level = "reviewer_l2"
	LevelL3 Level = "reviewer_l3"
)

// Levels lists the reviewer ranks in chain order.
func Levels() []Level { return []Level{LevelL1, LevelL2, LevelL3} }

// ValidLevel reports whether v names a reviewer rank.
func ValidLevel(v string) bool {
	for _, l := range Levels() {
		if string(l) == v {
			return true
		}
	}
	return false
}

// Event names what the trail records about one transition. It is defined here,
// beside the rules, on purpose: the gate is already deciding what kind of
// transition this is, and naming it is the same decision. Split across two
// packages, the name and the rule drift.
//
// Refusals have no Event. The trail says what happened, not what was attempted:
// a refused request changed nothing, and recording attempts would let anyone
// fill an auditee's history with noise from an endpoint they hold no rank on.
type Event string

const (
	EventSubmitted      Event = "workpaper_submitted"
	EventHandedOver     Event = "workpaper_handed_over"
	EventDraftAdopted   Event = "workpaper_draft_adopted"
	EventReviewPassed   Event = "workpaper_review_passed"
	EventReviewRejected Event = "workpaper_review_rejected"
	// EventFiled is deliberately not "passed at level three". An auditor reads
	// the trail looking for the moment the workpaper became immutable.
	EventFiled     Event = "workpaper_filed"
	EventCancelled Event = "workpaper_cancelled"
)

// DenyCode classifies a refusal so a transport can map it to a status and a UI
// can branch on it without parsing prose.
type DenyCode string

const (
	DenyIllegalTransition DenyCode = "illegal_transition"
	DenyLevelRequired     DenyCode = "level_required"
	DenySelfReview        DenyCode = "self_review"
	DenyFiled             DenyCode = "filed"
	DenyAgent             DenyCode = "agent_not_permitted"
	DenyLeavesChain       DenyCode = "leaves_chain"
)

// Input is everything the decision depends on. All values, all supplied by the
// caller; nothing here is looked up.
type Input struct {
	// InEngagement is what makes an issue a workpaper. An issue belonging to no
	// engagement is a remediation item or ordinary work and is never governed.
	// This is the state BEFORE the write.
	InEngagement bool
	// TargetInEngagement is whether the issue belongs to an engagement AFTER
	// the write. It differs from InEngagement when the write moves the issue
	// between projects, which is the one write that can change whether an issue
	// is a workpaper at all.
	TargetInEngagement bool
	// ProjectChanged reports that this write reassigns the issue's project,
	// including to or from none, and including between two engagements.
	ProjectChanged bool
	// From is the current status key; empty means the issue is being created.
	From string
	// To is the target status key, or empty when the write does not touch
	// status at all — a description or title edit is not a transition.
	To string
	// ActorMemberID identifies the acting member; empty for an agent.
	ActorMemberID string
	// ActorLevel is the actor's reviewer rank on THIS engagement, or empty.
	ActorLevel Level
	// ActorIsAdmin is true for a workspace owner or admin.
	ActorIsAdmin bool
	// ActorIsAgent is true when the write comes from an agent.
	ActorIsAgent bool
	// PreparerID is the member recorded as having submitted this workpaper for
	// review, or empty if it has never been submitted.
	PreparerID string
	// Reason is the free text accompanying the write. Required on a rejection
	// and ignored otherwise.
	Reason string
	// RefusedActor marks a caller with no standing in the auditee at all. The
	// collector sets it rather than deciding, so one place assembles the facts
	// and one place applies the rules.
	RefusedActor bool
}

// Decision is the answer. RecordPreparer asks the caller to snapshot the actor
// as the workpaper's preparer as part of the same write.
type Decision struct {
	Allowed        bool
	Code           DenyCode
	Reason         string
	RecordPreparer bool
	// Event is what the trail should record, or empty when this write is not a
	// transition worth recording.
	Event Event
	// RecordLevel is the rank that acted, for events where one did.
	RecordLevel Level
}

func allow() Decision { return Decision{Allowed: true} }

// record allows the write and asks the caller to record it.
func record(event Event, level Level) Decision {
	return Decision{Allowed: true, Event: event, RecordLevel: level}
}

func deny(code DenyCode, format string, args ...any) Decision {
	return Decision{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// levelFor maps a chain step to the rank entitled to make it. The rank that
// passes a workpaper ON from a status is the same one entitled to reject it,
// which is why rejection is not a separate table.
func levelFor(from string) (Level, bool) {
	switch from {
	case auditmode.StatusReviewL1:
		return LevelL1, true
	case auditmode.StatusReviewL2:
		return LevelL2, true
	case auditmode.StatusReviewL3:
		return LevelL3, true
	}
	return "", false
}

// nextIn maps a review status to the one it advances to.
func nextIn(from string) (string, bool) {
	switch from {
	case auditmode.StatusReviewL1:
		return auditmode.StatusReviewL2, true
	case auditmode.StatusReviewL2:
		return auditmode.StatusReviewL3, true
	case auditmode.StatusReviewL3:
		return auditmode.StatusFiled, true
	}
	return "", false
}

// Governs reports whether a transition between these two statuses could be
// governed at all. Callers use it to short-circuit BEFORE touching the
// database, so an issue write that cannot involve the review chain — which is
// almost every issue write on the platform — costs nothing.
//
// An empty `to` means the write does not change status; it can still be
// governed, because a filed workpaper refuses content edits too.
func Governs(from, to string) bool {
	return isAudit(from) || isAudit(to)
}

func isAudit(status string) bool {
	_, ok := auditmode.StatusByKey(status)
	return ok
}

// Decide answers one status write.
func Decide(in Input) Decision {
	if in.RefusedActor {
		return deny(DenyLevelRequired,
			"you are not a member of this auditee and cannot move its workpapers")
	}
	if !in.InEngagement && !in.TargetInEngagement {
		return allow()
	}

	// Engagement membership is what makes an issue a workpaper, so a write that
	// changes it can carry a workpaper out of the chain's reach entirely.
	// Deciding on the previous project alone leaves a two-request bypass: drop
	// the project while in review — a content edit, allowed — then set the
	// status on an issue the gate no longer recognises as a workpaper.
	if in.ProjectChanged {
		if in.InEngagement && isAudit(in.From) {
			return deny(DenyLeavesChain,
				"a workpaper in the review chain cannot be moved out of its engagement; take it out of the chain first")
		}
		effective := in.To
		if effective == "" {
			effective = in.From
		}
		if in.TargetInEngagement && isAudit(effective) && effective != auditmode.StatusDrafting {
			return deny(DenyIllegalTransition,
				"an issue carrying %q cannot be moved into an engagement; it would arrive as a workpaper no reviewer has seen", effective)
		}
	}
	if !in.InEngagement {
		// Joining an engagement is settled above; nothing else about this write
		// concerns a chain the issue was not yet in.
		return allow()
	}
	fromAudit := in.From != "" && isAudit(in.From)
	toAudit := isAudit(in.To)
	if !fromAudit && !toAudit {
		// Ordinary work inside an engagement, and the creation of a workpaper
		// on an ordinary status. Neither touches the chain.
		return allow()
	}

	// A filed workpaper is immutable. Checked before everything else so no
	// role, and no admin, reads as an exception to it — and before the
	// no-transition case below, because a content edit is still a write.
	if in.From == auditmode.StatusFiled {
		return deny(DenyFiled,
			"this workpaper is filed and cannot be changed; issue a corrected version instead")
	}

	// No status change: this write edits content. Freezing a workpaper the
	// moment it entered review would make the chain unusable, and reading the
	// empty target as a move out of the chain would produce a nonsense message.
	//
	// Re-sending the CURRENT status is the same thing. It matters because the
	// batch endpoint is all-or-nothing: read as a transition, one already-there
	// workpaper in a bulk selection would fail the whole request.
	if in.To == "" || in.To == in.From {
		return allow()
	}

	if in.To == "cancelled" {
		if in.ActorIsAgent {
			return deny(DenyAgent, "an agent cannot cancel a workpaper")
		}
		if in.ActorLevel == "" && !in.ActorIsAdmin {
			return deny(DenyLevelRequired,
				"cancelling a workpaper needs a reviewer role on this engagement, or workspace admin")
		}
		// The preparer cannot cancel their own workpaper, for the same reason
		// they cannot review it. Cancelling disposes of the work; leaving that
		// with the person who did it lets an inconvenient workpaper be taken
		// out of the chain by the one party the chain exists to check.
		if in.PreparerID != "" && in.ActorMemberID == in.PreparerID {
			return deny(DenySelfReview,
				"you prepared this workpaper and cannot cancel it; ask a reviewer on this engagement")
		}
		return record(EventCancelled, in.ActorLevel)
	}

	// Leaving the chain for any other ordinary status. This is the escape hatch
	// that matters most: review_l2 -> done is custom-to-built-in, and without
	// this branch it would walk straight out of the review chain.
	if fromAudit && !toAudit {
		return deny(DenyLeavesChain,
			"a workpaper cannot move from %q to %q; it stays in the review chain until it is filed or cancelled",
			in.From, in.To)
	}

	switch in.To {
	case auditmode.StatusDrafting:
		// Reached three ways: creating a workpaper, adopting an agent's draft,
		// and a rejection from any level. Only the last needs a rank.
		if level, isReview := levelFor(in.From); isReview {
			return decideChainStep(in, level)
		}
		if in.ActorIsAgent {
			return deny(DenyAgent, "an agent cannot move a workpaper to %q", in.To)
		}
		if in.From == auditmode.StatusAgentDelivered {
			return record(EventDraftAdopted, "")
		}
		return allow()

	case auditmode.StatusAgentDelivered:
		// A handover is an agent's explicit statement that its draft is ready
		// for a human. An agent run merely finishing is not one, and a human
		// must not be able to manufacture one.
		if !in.ActorIsAgent {
			return deny(DenyIllegalTransition,
				"%q is set by an agent handing over its draft, not by a member", in.To)
		}
		if in.From != auditmode.StatusDrafting {
			return deny(DenyIllegalTransition,
				"an agent can only hand over a workpaper that is in %q", auditmode.StatusDrafting)
		}
		return record(EventHandedOver, "")

	case auditmode.StatusReviewL1:
		if in.From != auditmode.StatusDrafting {
			return deny(DenyIllegalTransition,
				"a workpaper enters review from %q, not from %q", auditmode.StatusDrafting, in.From)
		}
		if in.ActorIsAgent {
			return deny(DenyAgent, "an agent cannot submit a workpaper for review")
		}
		// The submitter becomes the preparer. Ownership moves during review, so
		// this is the only moment the identity can be captured.
		return Decision{Allowed: true, RecordPreparer: true, Event: EventSubmitted}

	case auditmode.StatusReviewL2, auditmode.StatusReviewL3, auditmode.StatusFiled:
		level, isReview := levelFor(in.From)
		if !isReview {
			return deny(DenyIllegalTransition,
				"a workpaper cannot move from %q to %q; the review chain advances one level at a time",
				in.From, in.To)
		}
		if next, _ := nextIn(in.From); next != in.To {
			return deny(DenyIllegalTransition,
				"%q advances to %q, not to %q; the review chain advances one level at a time",
				in.From, next, in.To)
		}
		return decideChainStep(in, level)
	}

	return deny(DenyIllegalTransition, "%q is not a status a workpaper can move to from %q", in.To, in.From)
}

// decideChainStep authorizes a review decision — a pass or a rejection — made
// from a review status. Both need the same rank and both are closed to the
// preparer, which is what the chain exists to guarantee.
func decideChainStep(in Input, required Level) Decision {
	if in.ActorIsAgent {
		return deny(DenyAgent, "an agent cannot make a review decision")
	}
	if in.ActorLevel != required {
		return deny(DenyLevelRequired,
			"this step needs %s on this engagement", required)
	}
	if in.PreparerID != "" && in.ActorMemberID == in.PreparerID {
		return deny(DenySelfReview,
			"you prepared this workpaper and cannot review it; hand it to another %s", required)
	}
	if in.To == auditmode.StatusDrafting {
		// A rejection SHOULD carry a reason — telling a preparer their work does
		// not stand without saying why leaves them exactly where this control
		// exists to stop them being. It is not enforced here yet, and that is
		// deliberate: no client can send one today, and a server-side rule that
		// no client can satisfy would not make rejections better, it would make
		// them impossible, stranding every workpaper that fails review. The
		// requirement lands with the input that lets a reviewer type one.
		return record(EventReviewRejected, required)
	}
	if in.To == auditmode.StatusFiled {
		return record(EventFiled, required)
	}
	return record(EventReviewPassed, required)
}
