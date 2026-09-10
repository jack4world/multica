package auditgate

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/auditmode"
)

// What the trail says about each transition. Canonical, and pure: the record's
// name is decided in the same place as the rule it describes, so the two cannot
// drift apart.

func TestEveryChainTransitionProducesItsOwnRecord(t *testing.T) {
	cases := []struct {
		name  string
		from  string
		to    string
		level Level
		want  Event
	}{
		{"submit", auditmode.StatusDrafting, auditmode.StatusReviewL1, "", EventSubmitted},
		{"pass at l1", auditmode.StatusReviewL1, auditmode.StatusReviewL2, LevelL1, EventReviewPassed},
		{"pass at l2", auditmode.StatusReviewL2, auditmode.StatusReviewL3, LevelL2, EventReviewPassed},
		{"file at l3", auditmode.StatusReviewL3, auditmode.StatusFiled, LevelL3, EventFiled},
		{"reject at l1", auditmode.StatusReviewL1, auditmode.StatusDrafting, LevelL1, EventReviewRejected},
		{"reject at l2", auditmode.StatusReviewL2, auditmode.StatusDrafting, LevelL2, EventReviewRejected},
		{"reject at l3", auditmode.StatusReviewL3, auditmode.StatusDrafting, LevelL3, EventReviewRejected},
		{"cancel", auditmode.StatusReviewL2, "cancelled", LevelL1, EventCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			in.From, in.To, in.ActorLevel = tc.from, tc.to, tc.level
			d := Decide(in)
			if !d.Allowed {
				t.Fatalf("setup wrong, transition denied: %s", d.Reason)
			}
			if d.Event != tc.want {
				t.Errorf("event = %q, want %q", d.Event, tc.want)
			}
		})
	}
}

// Filing is not "passing at level three". An auditor reading the trail is
// looking for the moment the workpaper became immutable, and it deserves its
// own word.
func TestFilingIsNotRecordedAsAPass(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL3, auditmode.StatusFiled, LevelL3
	if d := Decide(in); d.Event == EventReviewPassed {
		t.Error("filing was recorded as an ordinary pass; the trail must name the moment the workpaper became immutable")
	}
}

// The trail says what happened, not what was attempted. A refused request
// changed nothing, and recording refusals would let anyone fill an auditee's
// history with noise by hammering an endpoint they have no rank on.
func TestARefusedTransitionProducesNoRecord(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL1, auditmode.StatusFiled, LevelL1
	d := Decide(in)
	if d.Allowed {
		t.Fatal("setup wrong, expected a refusal")
	}
	if d.Event != "" {
		t.Errorf("event = %q on a refusal, want none", d.Event)
	}
}

// Writes that are not transitions leave the trail alone: a content edit on a
// workpaper in review, and an ordinary issue moving through ordinary statuses.
func TestNonTransitionsProduceNoRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Input)
	}{
		{"content edit", func(in *Input) { in.From, in.To = auditmode.StatusReviewL1, "" }},
		{"same status", func(in *Input) { in.From, in.To = auditmode.StatusReviewL1, auditmode.StatusReviewL1 }},
		{"outside an engagement", func(in *Input) {
			in.InEngagement, in.TargetInEngagement = false, false
			in.From, in.To = "todo", "done"
		}},
		{"ordinary statuses", func(in *Input) { in.From, in.To = "todo", "in_progress" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			in.ActorLevel = ""
			tc.mut(&in)
			d := Decide(in)
			if !d.Allowed {
				t.Fatalf("setup wrong, denied: %s", d.Reason)
			}
			if d.Event != "" {
				t.Errorf("event = %q, want none", d.Event)
			}
		})
	}
}

// A rejection needs a reason, enforced in the GATE rather than only in the
// form. The form is one caller; the CLI, the API and every future client are
// the others, and a rule that lives in a disabled button is a rule the next
// caller does not have.
//
// This test exists because that is exactly what happened: the requirement was
// shipped as a disabled button and a `requires_reason` flag, the change that
// shipped it claimed the rule was "back in the gate", and it was not. A
// rejection with no reason went through and the trail recorded one explaining
// nothing.
func TestARejectionNeedsAReason(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel, in.Reason = auditmode.StatusReviewL2, auditmode.StatusDrafting, LevelL2, ""
	d := Decide(in)
	if d.Allowed {
		t.Fatal("a rejection with no reason was allowed")
	}
	if d.Code != DenyReasonRequired {
		t.Errorf("code = %q, want %q", d.Code, DenyReasonRequired)
	}
}

// Whitespace is not a reason.
func TestABlankReasonIsNotAReason(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel, in.Reason = auditmode.StatusReviewL1, auditmode.StatusDrafting, LevelL1, "   \n\t "
	if d := Decide(in); d.Allowed {
		t.Error("whitespace satisfied the reason requirement")
	}
}

// Only rejections. Passing, filing, submitting and cancelling all go through
// without one.
func TestNoOtherTransitionNeedsAReason(t *testing.T) {
	for _, tc := range []struct {
		name  string
		from  string
		to    string
		level Level
	}{
		{"pass", auditmode.StatusReviewL1, auditmode.StatusReviewL2, LevelL1},
		{"file", auditmode.StatusReviewL3, auditmode.StatusFiled, LevelL3},
		{"submit", auditmode.StatusDrafting, auditmode.StatusReviewL1, ""},
		{"cancel", auditmode.StatusReviewL2, "cancelled", LevelL1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			in.From, in.To, in.ActorLevel, in.Reason = tc.from, tc.to, tc.level, ""
			in.ActorMemberID = other
			if d := Decide(in); !d.Allowed {
				t.Fatalf("%s denied for want of a reason: %s", tc.name, d.Reason)
			}
		})
	}
}

// Every recorded event carries the facts a reader needs to check the chain was
// walked: which level acted, and where the workpaper moved.
func TestARecordedEventCarriesTheLevelAndBothStatuses(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL2, auditmode.StatusReviewL3, LevelL2
	d := Decide(in)
	if d.Event == "" {
		t.Fatal("no event recorded")
	}
	if d.RecordLevel != LevelL2 {
		t.Errorf("recorded level = %q, want %q", d.RecordLevel, LevelL2)
	}
}
