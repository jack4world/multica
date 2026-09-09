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

func TestAgentHandoverAndAdoptionAreDistinctEvents(t *testing.T) {
	handover := base()
	handover.From, handover.To = auditmode.StatusDrafting, auditmode.StatusAgentDelivered
	handover.ActorIsAgent, handover.ActorLevel, handover.PreparerID = true, "", ""
	if got := Decide(handover).Event; got != EventHandedOver {
		t.Errorf("handover event = %q, want %q", got, EventHandedOver)
	}

	adopt := base()
	adopt.From, adopt.To, adopt.ActorLevel, adopt.PreparerID = auditmode.StatusAgentDelivered, auditmode.StatusDrafting, "", ""
	if got := Decide(adopt).Event; got != EventDraftAdopted {
		t.Errorf("adoption event = %q, want %q", got, EventDraftAdopted)
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

// A rejection SHOULD carry a reason, and the trail records one when it is
// given. It is not yet REQUIRED, and that is a sequencing decision rather than
// an oversight: no client can send one today, so enforcing it here would not
// improve rejections — it would make them impossible, stranding every workpaper
// that fails review with no way back to its preparer.
func TestARejectionWithoutAReasonStillGoesThrough(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel, in.Reason = auditmode.StatusReviewL2, auditmode.StatusDrafting, LevelL2, ""
	d := Decide(in)
	if !d.Allowed {
		t.Fatalf("a rejection was blocked for want of a reason no client can send: %s", d.Reason)
	}
	if d.Event != EventReviewRejected {
		t.Errorf("event = %q, want %q", d.Event, EventReviewRejected)
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
