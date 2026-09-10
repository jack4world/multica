// Package auditreport decides who may move an 审计报告 through its sign-off
// chain, and renders one.
//
// Pure, like internal/auditgate and internal/remediate: no database, no
// request, no transaction. A report is the only thing anyone outside the audit
// function reads, so the rules about who signs it and what it says are the
// rules that most need to fit on one screen.
package auditreport

import (
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/auditgate"
)

// The chain. Three states, not a level-by-level review: a report is SIGNED, and
// the question the workpaper chain answers — did each level see it — is
// answered upstream, on the workpapers themselves.
const (
	StatusDrafting  = "drafting"
	StatusReviewing = "reviewing"
	StatusIssued    = "issued"
)

// Statuses lists the chain in order.
func Statuses() []string { return []string{StatusDrafting, StatusReviewing, StatusIssued} }

// ValidStatus reports whether v names a report state.
func ValidStatus(v string) bool {
	for _, s := range Statuses() {
		if s == v {
			return true
		}
	}
	return false
}

// Event names what the trail records. Refusals have no event.
type Event string

const (
	EventSubmitted Event = "report_submitted"
	EventIssued    Event = "report_issued"
	EventReturned  Event = "report_returned"
)

// DenyCode classifies a refusal.
type DenyCode string

const (
	DenyIllegalTransition DenyCode = "illegal_transition"
	DenyRankRequired      DenyCode = "rank_required"
	DenyIssued            DenyCode = "report_issued"
	DenyAgent             DenyCode = "agent_not_permitted"
	DenyReasonRequired    DenyCode = "reason_required"
)

// Input is everything a decision depends on.
type Input struct {
	From string
	// To is the target state, or empty when the write edits the report's text
	// rather than moving it.
	To string
	// ActorLevel is the actor's reviewer rank on THIS engagement, or empty.
	ActorLevel auditgate.Level
	// ActorIsAdmin is true for a workspace owner or admin.
	ActorIsAdmin bool
	// ActorIsAgent is true when the write comes from an agent.
	ActorIsAgent bool
	// ReviewLevels is how many levels this engagement runs. The TOP rank of
	// that depth is who signs — a two-level engagement must not wait for a
	// level it does not have.
	ReviewLevels int
	// Reason accompanies a send-back.
	Reason string
	// RefusedActor marks a caller with no standing in the auditee.
	RefusedActor bool
}

// Decision is the answer.
type Decision struct {
	Allowed bool
	Code    DenyCode
	Reason  string
	Event   Event
}

func allow() Decision         { return Decision{Allowed: true} }
func record(e Event) Decision { return Decision{Allowed: true, Event: e} }
func deny(c DenyCode, f string, a ...any) Decision {
	return Decision{Code: c, Reason: fmt.Sprintf(f, a...)}
}

// SigningLevel is the rank that signs a report on an engagement of this depth.
// At depth two that is 项目经理; at depth three, 部门负责人. Reusing the depth
// decision (docs/adr/0003) rather than adding a second answer to "who is senior
// here".
func SigningLevel(depth int) auditgate.Level {
	levels := auditgate.LevelsUpTo(depth)
	return levels[len(levels)-1]
}

// Decide answers one write to a report.
func Decide(in Input) Decision {
	if in.RefusedActor {
		return deny(DenyRankRequired,
			"you are not a member of this auditee and cannot act on its reports")
	}

	// An issued report is the document that went out. Checked before everything
	// else so no rank and no admin reads as an exception to it, and before the
	// no-transition case below, because a text edit is still a write.
	if in.From == StatusIssued {
		return deny(DenyIssued,
			"this report is issued and cannot be changed; issue a new version instead")
	}

	if in.To == "" || in.To == in.From {
		// Editing the text of an unsigned report. Anyone who can see the
		// engagement can write it; what needs a rank is sending it out.
		return allow()
	}
	if !ValidStatus(in.To) {
		return deny(DenyIllegalTransition, "%q is not a state an audit report has", in.To)
	}

	switch in.To {
	case StatusReviewing:
		if in.From != StatusDrafting {
			return deny(DenyIllegalTransition,
				"a report is submitted for sign-off from %q, not from %q", StatusDrafting, in.From)
		}
		if in.ActorIsAgent {
			return deny(DenyAgent, "an agent cannot submit a report for sign-off")
		}
		return record(EventSubmitted)

	case StatusIssued:
		if in.From != StatusReviewing {
			return deny(DenyIllegalTransition,
				"a report is issued from %q, not from %q; issuing is a signature, not a state anyone can set",
				StatusReviewing, in.From)
		}
		if in.ActorIsAgent {
			return deny(DenyAgent, "an agent cannot issue a report")
		}
		required := SigningLevel(in.ReviewLevels)
		// Admin is NOT a way around the signature. Workspace admin is an
		// administrative role; signing the department's report is not an
		// administrative act.
		if in.ActorLevel != required {
			return deny(DenyRankRequired,
				"issuing this engagement's report needs %s", required)
		}
		return record(EventIssued)

	case StatusDrafting:
		if in.From != StatusReviewing {
			return deny(DenyIllegalTransition,
				"a report returns to %q from %q, not from %q", StatusDrafting, StatusReviewing, in.From)
		}
		if in.ActorIsAgent {
			return deny(DenyAgent, "an agent cannot return a report")
		}
		if in.ActorLevel != SigningLevel(in.ReviewLevels) {
			return deny(DenyRankRequired,
				"returning this engagement's report needs %s", SigningLevel(in.ReviewLevels))
		}
		// Telling the drafter their report does not stand without saying why
		// leaves them where this control exists to stop them being, and the
		// trail entry then records a return that explains nothing.
		if strings.TrimSpace(in.Reason) == "" {
			return deny(DenyReasonRequired,
				"returning a report needs a reason; the drafter has no other way to know what to change")
		}
		return record(EventReturned)
	}

	return deny(DenyIllegalTransition, "%q is not a state this report can move to from %q", in.To, in.From)
}
