package auditgate

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/auditmode"
)

// What the interface is allowed to offer. Canonical and pure: the buttons a
// reviewer sees are derived from the same matrix that enforces the chain, so
// they cannot drift from it.

func keys(actions []Action) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, a.To)
	}
	return out
}

func TestAReviewerAtTheCurrentLevelGetsPassAndReturn(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL1, "", LevelL1
	got := keys(Available(in))
	want := map[string]bool{auditmode.StatusReviewL2: true, auditmode.StatusDrafting: true, "cancelled": true}
	for _, k := range got {
		if !want[k] {
			t.Errorf("offered %q, which is not a step this reviewer can take", k)
		}
	}
	if len(got) != len(want) {
		t.Errorf("offered %v, want pass, return and cancel", got)
	}
}

func TestTheThirdLevelIsOfferedFilingNotAnotherPass(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL3, "", LevelL3
	for _, a := range Available(in) {
		if a.To == auditmode.StatusFiled && a.Event != EventFiled {
			t.Errorf("filing offered as %q; the action that makes a workpaper immutable has to say so", a.Event)
		}
	}
	if !containsTo(Available(in), auditmode.StatusFiled) {
		t.Error("the third level was not offered filing")
	}
}

// Independence made visible: the preparer is not shown the buttons, not merely
// refused when they press one.
func TestThePreparerIsOfferedNothingOnTheirOwnWorkpaper(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL1, "", LevelL1
	in.ActorMemberID = preparer
	if actions := Available(in); len(actions) != 0 {
		t.Errorf("offered %v to the preparer of the workpaper", keys(actions))
	}
}

func TestAMemberWithNoRankIsOfferedNothingInReview(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL2, "", ""
	if actions := Available(in); len(actions) != 0 {
		t.Errorf("offered %v to someone holding no rank", keys(actions))
	}
}

// The absence of buttons on a filed workpaper is the rule showing through the
// interface, not a bug.
func TestAFiledWorkpaperOffersNothingToAnyone(t *testing.T) {
	for _, level := range []Level{LevelL1, LevelL2, LevelL3, ""} {
		in := base()
		in.From, in.To, in.ActorLevel, in.ActorIsAdmin = auditmode.StatusFiled, "", level, true
		if actions := Available(in); len(actions) != 0 {
			t.Errorf("filed workpaper offered %v to %q", keys(actions), level)
		}
	}
}

func TestTheOwnerOfADraftIsOfferedSubmission(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel, in.PreparerID = auditmode.StatusDrafting, "", "", ""
	if !containsTo(Available(in), auditmode.StatusReviewL1) {
		t.Errorf("a workpaper in drafting offered %v, want submission", keys(Available(in)))
	}
}

func TestAnActionSaysWhetherItNeedsAReason(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL2, "", LevelL2
	for _, a := range Available(in) {
		wantReason := a.To == auditmode.StatusDrafting
		if a.RequiresReason != wantReason {
			t.Errorf("action to %q: requires_reason = %v, want %v", a.To, a.RequiresReason, wantReason)
		}
	}
}

// THE property. An interface that offers a button the server would refuse is
// worse than no button: it teaches people the control is arbitrary. Generated
// over every rank against every status rather than spot-checked.
func TestEveryOfferedActionWouldBeAccepted(t *testing.T) {
	statuses := []string{
		auditmode.StatusDrafting,
		auditmode.StatusAgentDelivered,
		auditmode.StatusReviewL1,
		auditmode.StatusReviewL2,
		auditmode.StatusReviewL3,
		auditmode.StatusFiled,
	}
	levels := []Level{"", LevelL1, LevelL2, LevelL3}
	actors := []string{preparer, other}

	for _, from := range statuses {
		for _, level := range levels {
			for _, actor := range actors {
				for _, admin := range []bool{false, true} {
					in := base()
					in.From, in.To = from, ""
					in.ActorLevel, in.ActorMemberID, in.ActorIsAdmin = level, actor, admin
					for _, a := range Available(in) {
						check := in
						check.To = a.To
						if a.RequiresReason {
							check.Reason = "because"
						}
						if d := Decide(check); !d.Allowed {
							t.Errorf("offered %s -> %s to level %q (admin=%v, self=%v) but the gate refuses it: %s",
								from, a.To, level, admin, actor == preparer, d.Reason)
						}
					}
				}
			}
		}
	}
}

// The other half: an action the gate WOULD accept should be offered, or the
// interface silently strands people who have to fall back to the raw API.
func TestEveryAcceptedTransitionIsOffered(t *testing.T) {
	statuses := []string{
		auditmode.StatusDrafting,
		auditmode.StatusAgentDelivered,
		auditmode.StatusReviewL1,
		auditmode.StatusReviewL2,
		auditmode.StatusReviewL3,
	}
	for _, from := range statuses {
		for _, level := range []Level{"", LevelL1, LevelL2, LevelL3} {
			in := base()
			in.From, in.To, in.ActorLevel = from, "", level
			offered := map[string]bool{}
			for _, a := range Available(in) {
				offered[a.To] = true
			}
			for _, to := range append(append([]string{}, statuses...), auditmode.StatusFiled, "cancelled") {
				if to == from {
					continue
				}
				check := in
				check.To, check.Reason = to, "because"
				if d := Decide(check); d.Allowed && d.Event != "" && !offered[to] {
					t.Errorf("the gate accepts %s -> %s at level %q but it is not offered", from, to, level)
				}
			}
		}
	}
}

// Agents do not get an interface, and must never be offered one.
func TestAnAgentIsOfferedNoReviewActions(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorIsAgent, in.ActorLevel = auditmode.StatusReviewL1, "", true, LevelL1
	for _, a := range Available(in) {
		if a.To != auditmode.StatusAgentDelivered {
			t.Errorf("an agent was offered %q", a.To)
		}
	}
}

func TestNothingIsOfferedOutsideAnEngagement(t *testing.T) {
	in := base()
	in.InEngagement, in.TargetInEngagement = false, false
	in.From, in.To = "todo", ""
	if actions := Available(in); len(actions) != 0 {
		t.Errorf("offered %v on an issue that is not a workpaper", keys(actions))
	}
}

func containsTo(actions []Action, to string) bool {
	for _, a := range actions {
		if a.To == to {
			return true
		}
	}
	return false
}
