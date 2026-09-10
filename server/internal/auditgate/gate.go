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
	"strings"

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

// DefaultReviewLevels is what an engagement runs unless it says otherwise.
//
// TWO, not three. 三级复核 is a CPA firm's quality-control rule; a company's
// internal audit function typically runs 主审复核 → 部门负责人审定. A department
// with two levels that is forced to invent a third reviewer produces a
// signature that satisfies the software and nobody else — which teaches
// everyone involved that the whole chain is theatre.
const DefaultReviewLevels = 2

// MaxReviewLevels bounds the chain. Each level is a status in a catalog people
// read, and nothing asks for a fourth.
const MaxReviewLevels = 3

// Levels lists the reviewer ranks in chain order, up to the maximum.
func Levels() []Level { return []Level{LevelL1, LevelL2, LevelL3} }

// LevelsUpTo lists the ranks an engagement of this depth actually runs.
func LevelsUpTo(depth int) []Level {
	return Levels()[:normalizeDepth(depth)]
}

// normalizeDepth treats an unset or out-of-range depth as the default, so a
// caller that forgot to read the engagement gets the ordinary chain rather than
// no chain at all.
func normalizeDepth(depth int) int {
	if depth < 1 || depth > MaxReviewLevels {
		return DefaultReviewLevels
	}
	return depth
}

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
	DenyReasonRequired    DenyCode = "reason_required"
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
	// ReviewLevels is how many levels THIS engagement runs, 1..3. Zero means
	// the caller did not set it and the default applies.
	ReviewLevels int
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

// reviewStatuses are the chain's review stages in order. The catalog seeds all
// three in every auditee because it is workspace-level and one auditee can hold
// engagements at different depths; which of them an engagement can REACH is
// what the depth decides.
var reviewStatuses = []string{
	auditmode.StatusReviewL1,
	auditmode.StatusReviewL2,
	auditmode.StatusReviewL3,
}

// ReviewStatusForLevel is the status a rank reviews at.
func ReviewStatusForLevel(level Level) string {
	for i, l := range Levels() {
		if l == level {
			return reviewStatuses[i]
		}
	}
	return ""
}

// ordinalOf reports which review stage a status is, 1-based, and whether it is
// one at all.
func ordinalOf(status string) (int, bool) {
	for i, s := range reviewStatuses {
		if s == status {
			return i + 1, true
		}
	}
	return 0, false
}

// levelFor maps a chain step to the rank entitled to make it, within an
// engagement of this depth. A stage beyond the depth is not part of this
// engagement's chain at all.
//
// The rank that passes a workpaper ON from a stage is the same one entitled to
// reject it, which is why rejection is not a separate table.
func levelFor(from string, depth int) (Level, bool) {
	ordinal, ok := ordinalOf(from)
	if !ok || ordinal > normalizeDepth(depth) {
		return "", false
	}
	return Levels()[ordinal-1], true
}

// nextIn maps a review stage to what it advances to.
//
// THE generalisation: the LAST level files. At depth two, 项目经理 files; at
// depth one, 主审 does. Nobody is asked to invent a reviewer to get a workpaper
// archived.
func nextIn(from string, depth int) (string, bool) {
	ordinal, ok := ordinalOf(from)
	depth = normalizeDepth(depth)
	if !ok || ordinal > depth {
		return "", false
	}
	if ordinal == depth {
		return auditmode.StatusFiled, true
	}
	return reviewStatuses[ordinal], true
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
		// Reached two ways: creating a workpaper, and a rejection from any
		// level. Only the second needs a rank.
		if level, isReview := levelFor(in.From, in.ReviewLevels); isReview {
			return decideChainStep(in, level)
		}
		if in.ActorIsAgent {
			return deny(DenyAgent, "an agent cannot move a workpaper to %q", in.To)
		}
		return allow()

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
		if ordinal, isReview := ordinalOf(in.To); isReview && ordinal > normalizeDepth(in.ReviewLevels) {
			// Named rather than reported as a generic illegal transition: the
			// reader needs to know this engagement has fewer levels, not that
			// they picked a step out of order.
			return deny(DenyIllegalTransition,
				"this engagement runs %d review levels, so %q is not part of its chain",
				normalizeDepth(in.ReviewLevels), in.To)
		}
		level, isReview := levelFor(in.From, in.ReviewLevels)
		if !isReview {
			return deny(DenyIllegalTransition,
				"a workpaper cannot move from %q to %q; the review chain advances one level at a time",
				in.From, in.To)
		}
		if next, _ := nextIn(in.From, in.ReviewLevels); next != in.To {
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
		// A rejection needs a reason. Telling a preparer their work does not
		// stand without saying why leaves them exactly where this control exists
		// to stop them being, and the trail entry then records a rejection that
		// explains nothing to whoever reads the file later.
		//
		// Enforced HERE, not only in the form: the form is one caller. The CLI,
		// the API and every future client are the others, and a rule that lives
		// in a disabled button is a rule the next caller does not have.
		if strings.TrimSpace(in.Reason) == "" {
			return deny(DenyReasonRequired,
				"returning a workpaper to its preparer needs a reason; they have no other way to know what to fix")
		}
		return record(EventReviewRejected, required)
	}
	if in.To == auditmode.StatusFiled {
		return record(EventFiled, required)
	}
	return record(EventReviewPassed, required)
}
