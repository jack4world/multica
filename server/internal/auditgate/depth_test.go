package auditgate

import (
	"fmt"
	"testing"

	"github.com/multica-ai/multica/server/internal/auditmode"
)

// The chain is generated from the engagement's review depth. Three levels is a
// CPA firm's rule; a company's internal audit function usually runs two, and a
// department forced to invent a third reviewer learns that the whole chain is
// theatre.

func atDepth(depth int) Input {
	in := base()
	in.ReviewLevels = depth
	return in
}

// THE generalisation: the LAST level files, whatever the depth. Nobody is asked
// to invent a reviewer to get a workpaper archived.
func TestTheLastLevelFilesAtEveryDepth(t *testing.T) {
	for _, tc := range []struct {
		depth int
		from  string
		level Level
	}{
		{1, auditmode.StatusReviewL1, LevelL1},
		{2, auditmode.StatusReviewL2, LevelL2},
		{3, auditmode.StatusReviewL3, LevelL3},
	} {
		t.Run(fmt.Sprintf("depth %d", tc.depth), func(t *testing.T) {
			in := atDepth(tc.depth)
			in.From, in.To, in.ActorLevel = tc.from, auditmode.StatusFiled, tc.level
			d := Decide(in)
			if !d.Allowed {
				t.Fatalf("the last level could not file: %s", d.Reason)
			}
			if d.Event != EventFiled {
				t.Errorf("event = %q, want %q", d.Event, EventFiled)
			}
		})
	}
}

func TestAnEarlierLevelPassesOnRatherThanFiling(t *testing.T) {
	in := atDepth(3)
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL1, auditmode.StatusFiled, LevelL1
	if d := Decide(in); d.Allowed {
		t.Error("the first level filed a workpaper on a three-level engagement")
	}
	in.To = auditmode.StatusReviewL2
	if d := Decide(in); !d.Allowed {
		t.Errorf("the first level could not pass on: %s", d.Reason)
	}
}

// A level the engagement does not run is not reachable, and the refusal says
// why rather than reporting a generic illegal transition.
func TestALevelBeyondTheDepthIsUnreachable(t *testing.T) {
	in := atDepth(2)
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL2, auditmode.StatusReviewL3, LevelL2
	d := Decide(in)
	if d.Allowed {
		t.Fatal("a two-level engagement advanced into 三级复核")
	}
	if d.Code != DenyIllegalTransition {
		t.Errorf("code = %q, want %q", d.Code, DenyIllegalTransition)
	}
}

func TestSubmissionAlwaysEntersAtTheFirstLevel(t *testing.T) {
	for depth := 1; depth <= 3; depth++ {
		in := atDepth(depth)
		in.From, in.To, in.ActorLevel, in.PreparerID = auditmode.StatusDrafting, auditmode.StatusReviewL1, "", ""
		if d := Decide(in); !d.Allowed {
			t.Errorf("depth %d: submission refused: %s", depth, d.Reason)
		}
	}
}

// Rejection is unchanged at every level and every depth: back to the preparer.
func TestRejectionReturnsToThePreparerAtEveryDepth(t *testing.T) {
	for _, tc := range []struct {
		depth int
		from  string
		level Level
	}{
		{1, auditmode.StatusReviewL1, LevelL1},
		{2, auditmode.StatusReviewL2, LevelL2},
		{3, auditmode.StatusReviewL3, LevelL3},
	} {
		in := atDepth(tc.depth)
		in.From, in.To, in.ActorLevel = tc.from, auditmode.StatusDrafting, tc.level
		if d := Decide(in); !d.Allowed {
			t.Errorf("depth %d from %s: rejection refused: %s", tc.depth, tc.from, d.Reason)
		}
	}
}

// The interface offers only steps this engagement has.
func TestNothingBeyondTheDepthIsOffered(t *testing.T) {
	in := atDepth(2)
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL2, "", LevelL2
	for _, a := range Available(in) {
		if a.To == auditmode.StatusReviewL3 {
			t.Error("三级复核 was offered on a two-level engagement")
		}
	}
	if !containsTo(Available(in), auditmode.StatusFiled) {
		t.Error("the last level of a two-level engagement was not offered filing")
	}
}

// The substance of generating the matrix from a number: a depth must never be a
// shape the gate has not been tested in. These are the same two properties the
// chain already carries, now run at every depth.
func TestTheGeneratedPropertiesHoldAtEveryDepth(t *testing.T) {
	statuses := []string{
		auditmode.StatusDrafting,
		auditmode.StatusReviewL1,
		auditmode.StatusReviewL2,
		auditmode.StatusReviewL3,
		auditmode.StatusFiled,
	}
	levels := []Level{"", LevelL1, LevelL2, LevelL3}

	for depth := 1; depth <= 3; depth++ {
		for _, from := range statuses {
			for _, level := range levels {
				for _, actor := range []string{preparer, other} {
					in := atDepth(depth)
					in.From, in.To = from, ""
					in.ActorLevel, in.ActorMemberID = level, actor

					offered := map[string]bool{}
					for _, a := range Available(in) {
						offered[a.To] = true
						check := in
						check.To = a.To
						if a.RequiresReason {
							check.Reason = "because"
						}
						if d := Decide(check); !d.Allowed {
							t.Errorf("depth %d: offered %s -> %s at %q but the gate refuses it: %s",
								depth, from, a.To, level, d.Reason)
						}
					}
					for _, to := range append(append([]string{}, statuses...), "cancelled") {
						if to == from {
							continue
						}
						check := in
						check.To, check.Reason = to, "because"
						if d := Decide(check); d.Allowed && d.Event != "" && !offered[to] {
							t.Errorf("depth %d: the gate accepts %s -> %s at %q but it is not offered",
								depth, from, to, level)
						}
					}
				}
			}
		}
	}
}

// A depth the caller did not set must not silently mean "no chain at all".
func TestAnUnsetDepthFallsBackToTheDefault(t *testing.T) {
	in := base()
	in.ReviewLevels = 0
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL2, auditmode.StatusFiled, LevelL2
	if d := Decide(in); !d.Allowed {
		t.Errorf("an unset depth did not behave as the default of %d: %s", DefaultReviewLevels, d.Reason)
	}
}

func TestTheDefaultIsTwoLevels(t *testing.T) {
	// The whole point of this piece, and one character away from being three.
	if DefaultReviewLevels != 2 {
		t.Errorf("DefaultReviewLevels = %d, want 2 — a company's internal audit function "+
			"runs two levels, and forcing a third means inventing a reviewer", DefaultReviewLevels)
	}
}
