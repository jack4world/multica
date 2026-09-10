// Package auditgate's decision table. This file is the CANONICAL home for the
// review chain's rules: every cell of the matrix and every denial reason is
// exercised here, without a database. The handler suite covers wiring,
// permissions and the named batch regression, and does not replay this table.
package auditgate

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/auditmode"
)

const (
	preparer = "11111111-1111-1111-1111-111111111111"
	other    = "22222222-2222-2222-2222-222222222222"
	third    = "33333333-3333-3333-3333-333333333333"
)

// base is a submitted workpaper sitting at one_level review, acted on by a
// reviewer who is not its preparer. Each test varies only what it is about.
func base() Input {
	return Input{
		InEngagement:  true,
		From:          auditmode.StatusReviewL1,
		To:            auditmode.StatusReviewL2,
		ActorMemberID: other,
		ActorLevel:    LevelL1,
		PreparerID:    preparer,
		// Present by default so tests about anything else are unaffected by the
		// rejection-needs-a-reason rule; the tests about that rule clear it.
		Reason: "does not stand",
	}
}

// Engagement membership is what makes an issue a workpaper, so a write that
// CHANGES it is a write that can carry a workpaper out of the chain. Deciding
// on the previous project alone leaves a two-request bypass: drop the project
// while in review (a content edit, allowed), then set the status to filed on an
// issue the gate no longer recognises.
func TestAWorkpaperCannotLeaveItsEngagementWhileInTheChain(t *testing.T) {
	for _, from := range []string{
		auditmode.StatusDrafting,
		auditmode.StatusReviewL2,
	} {
		t.Run(from, func(t *testing.T) {
			in := base()
			in.From, in.To = from, ""
			in.ProjectChanged, in.TargetInEngagement = true, false
			in.ActorLevel, in.ActorIsAdmin = LevelL3, true
			if d := Decide(in); d.Allowed {
				t.Fatalf("a workpaper at %s was moved out of its engagement; the next status write would not be governed at all", from)
			}
		})
	}
}

// The mirror image, which defeats the create-time guard: create an issue at
// filed with no project, then move it into the engagement.
func TestAnIssueCannotBeMovedIntoAnEngagementCarryingAReviewStatus(t *testing.T) {
	for _, status := range []string{
		auditmode.StatusFiled,
		auditmode.StatusReviewL3,
	} {
		t.Run(status, func(t *testing.T) {
			in := base()
			in.InEngagement, in.TargetInEngagement, in.ProjectChanged = false, true, true
			in.From, in.To = status, ""
			in.ActorLevel, in.ActorIsAdmin = LevelL3, true
			if d := Decide(in); d.Allowed {
				t.Fatalf("an issue at %s was moved into an engagement, arriving as a workpaper no reviewer ever saw", status)
			}
		})
	}
}

func TestAnOrdinaryIssueCanStillBeMovedIntoAnEngagement(t *testing.T) {
	for _, status := range []string{"todo", "in_progress", auditmode.StatusDrafting} {
		t.Run(status, func(t *testing.T) {
			in := base()
			in.InEngagement, in.TargetInEngagement, in.ProjectChanged = false, true, true
			in.From, in.To = status, ""
			in.ActorLevel, in.PreparerID = "", ""
			if d := Decide(in); !d.Allowed {
				t.Fatalf("moving an issue at %s into an engagement was denied: %s", status, d.Reason)
			}
		})
	}
}

// Re-sending a workpaper's current status is not a transition. The batch
// endpoint is all-or-nothing, so treating it as one would fail a whole bulk
// update because a single selected workpaper already sat on the target.
func TestReSendingTheCurrentStatusIsANoOp(t *testing.T) {
	for _, status := range []string{
		auditmode.StatusDrafting,
		auditmode.StatusReviewL1,
		auditmode.StatusReviewL3,
	} {
		t.Run(status, func(t *testing.T) {
			in := base()
			in.From, in.To, in.ActorLevel = status, status, ""
			if d := Decide(in); !d.Allowed {
				t.Fatalf("re-sending %s was refused as a transition: %s", status, d.Reason)
			}
		})
	}
}

// Except on a filed workpaper, where every write is refused.
func TestReSendingFiledIsStillRefused(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusFiled, auditmode.StatusFiled
	if d := Decide(in); d.Allowed {
		t.Fatal("a filed workpaper accepted a write")
	}
}

func TestOutsideAnEngagementNothingIsGoverned(t *testing.T) {
	// A remediation item is an issue that belongs to no engagement. It must
	// move through ordinary statuses untouched, or the vertical would drag
	// every issue in an auditee into the review chain.
	in := base()
	in.InEngagement = false
	in.From = "todo"
	in.To = "done"
	if d := Decide(in); !d.Allowed {
		t.Fatalf("denied a non-workpaper transition: %s", d.Reason)
	}
}

func TestTransitionsWithNoAuditStatusOnEitherSideAreUngoverned(t *testing.T) {
	in := base()
	in.From = "todo"
	in.To = "in_progress"
	in.ActorLevel = ""
	if d := Decide(in); !d.Allowed {
		t.Fatalf("denied an ordinary transition inside an engagement: %s", d.Reason)
	}
}

func TestTheReviewChainAdvancesOneLevelAtATime(t *testing.T) {
	cases := []struct {
		name  string
		from  string
		to    string
		level Level
	}{
		{"l1 passes", auditmode.StatusReviewL1, auditmode.StatusReviewL2, LevelL1},
		{"l2 passes", auditmode.StatusReviewL2, auditmode.StatusReviewL3, LevelL2},
		{"l3 files", auditmode.StatusReviewL3, auditmode.StatusFiled, LevelL3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			in.From, in.To, in.ActorLevel = tc.from, tc.to, tc.level
			if d := Decide(in); !d.Allowed {
				t.Fatalf("%s -> %s by %s denied: %s", tc.from, tc.to, tc.level, d.Reason)
			}
		})
	}
}

func TestALevelCannotActAtAnotherLevel(t *testing.T) {
	cases := []struct {
		name  string
		from  string
		to    string
		level Level
	}{
		{"l2 cannot do l1's pass", auditmode.StatusReviewL1, auditmode.StatusReviewL2, LevelL2},
		{"l1 cannot do l2's pass", auditmode.StatusReviewL2, auditmode.StatusReviewL3, LevelL1},
		{"l2 cannot file", auditmode.StatusReviewL3, auditmode.StatusFiled, LevelL2},
		{"no role at all", auditmode.StatusReviewL1, auditmode.StatusReviewL2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			in.From, in.To, in.ActorLevel = tc.from, tc.to, tc.level
			d := Decide(in)
			if d.Allowed {
				t.Fatalf("%s -> %s allowed for level %q", tc.from, tc.to, tc.level)
			}
			if d.Code != DenyLevelRequired {
				t.Errorf("code = %q, want %q", d.Code, DenyLevelRequired)
			}
		})
	}
}

// The denial has to name the level the transition needs, because that message
// is what tells the reviewer who to hand the workpaper to.
func TestALevelDenialNamesTheLevelRequired(t *testing.T) {
	in := base()
	in.ActorLevel = LevelL2
	d := Decide(in)
	if !strings.Contains(d.Reason, string(LevelL1)) {
		t.Errorf("reason = %q, want it to name %q", d.Reason, LevelL1)
	}
}

// Independence is the whole point of the chain. Holding the right level is not
// enough if you are the person who wrote the workpaper.
func TestThePreparerCannotReviewTheirOwnWorkpaper(t *testing.T) {
	for _, tc := range []struct {
		name  string
		from  string
		to    string
		level Level
	}{
		{"pass at l1", auditmode.StatusReviewL1, auditmode.StatusReviewL2, LevelL1},
		{"pass at l2", auditmode.StatusReviewL2, auditmode.StatusReviewL3, LevelL2},
		{"file at l3", auditmode.StatusReviewL3, auditmode.StatusFiled, LevelL3},
		{"reject at l1", auditmode.StatusReviewL1, auditmode.StatusDrafting, LevelL1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			in.From, in.To, in.ActorLevel = tc.from, tc.to, tc.level
			in.ActorMemberID = preparer
			d := Decide(in)
			if d.Allowed {
				t.Fatal("the preparer was allowed to review their own workpaper")
			}
			if d.Code != DenySelfReview {
				t.Errorf("code = %q, want %q", d.Code, DenySelfReview)
			}
		})
	}
}

// Rejection returns to the preparer, never to the level below: a rejection
// says the workpaper does not stand, not that the previous reviewer erred, and
// stepping back one level produces a loop where that level re-approves.
func TestEveryLevelRejectsBackToDrafting(t *testing.T) {
	for _, tc := range []struct {
		from  string
		level Level
	}{
		{auditmode.StatusReviewL1, LevelL1},
		{auditmode.StatusReviewL2, LevelL2},
		{auditmode.StatusReviewL3, LevelL3},
	} {
		t.Run(tc.from, func(t *testing.T) {
			in := base()
			in.From, in.To, in.ActorLevel = tc.from, auditmode.StatusDrafting, tc.level
			if d := Decide(in); !d.Allowed {
				t.Fatalf("rejection from %s denied: %s", tc.from, d.Reason)
			}
		})
	}
}

func TestAReviewerCannotStepAWorkpaperBackOneLevel(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = auditmode.StatusReviewL3, auditmode.StatusReviewL2, LevelL3
	d := Decide(in)
	if d.Allowed {
		t.Fatal("review_l3 -> review_l2 allowed; rejection must return to the preparer")
	}
	if d.Code != DenyIllegalTransition {
		t.Errorf("code = %q, want %q", d.Code, DenyIllegalTransition)
	}
}

func TestALevelCannotBeSkipped(t *testing.T) {
	for _, tc := range []struct {
		name string
		from string
		to   string
	}{
		{"drafting straight to filed", auditmode.StatusDrafting, auditmode.StatusFiled},
		{"l1 straight to filed", auditmode.StatusReviewL1, auditmode.StatusFiled},
		{"l1 straight to l3", auditmode.StatusReviewL1, auditmode.StatusReviewL3},
		{"drafting straight to l3", auditmode.StatusDrafting, auditmode.StatusReviewL3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			in.From, in.To = tc.from, tc.to
			in.ActorLevel = LevelL3
			if d := Decide(in); d.Allowed {
				t.Fatalf("%s -> %s allowed; the chain must not be skippable", tc.from, tc.to)
			}
		})
	}
}

func TestSubmittingForReviewRecordsTheSubmitterAsPreparer(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusDrafting, auditmode.StatusReviewL1
	in.ActorLevel = ""
	in.ActorMemberID = third
	in.PreparerID = ""
	d := Decide(in)
	if !d.Allowed {
		t.Fatalf("submitting for review denied: %s", d.Reason)
	}
	if !d.RecordPreparer {
		t.Error("submitting for review must snapshot the submitter as preparer; " +
			"without it the no-self-review rule has no input")
	}
}

// Ownership moves during review, so the preparer cannot be read back off the
// issue afterwards. Only the submission transition writes it.
func TestNoOtherTransitionRecordsAPreparer(t *testing.T) {
	for _, tc := range []struct {
		from  string
		to    string
		level Level
	}{
		{auditmode.StatusReviewL1, auditmode.StatusReviewL2, LevelL1},
		{auditmode.StatusReviewL3, auditmode.StatusFiled, LevelL3},
		{auditmode.StatusReviewL1, auditmode.StatusDrafting, LevelL1},
	} {
		in := base()
		in.From, in.To, in.ActorLevel = tc.from, tc.to, tc.level
		if d := Decide(in); d.RecordPreparer {
			t.Errorf("%s -> %s re-recorded the preparer", tc.from, tc.to)
		}
	}
}

func TestAFiledWorkpaperRejectsEveryTransition(t *testing.T) {
	for _, to := range []string{
		auditmode.StatusDrafting,
		auditmode.StatusReviewL1,
		auditmode.StatusReviewL3,
		"done",
		"cancelled",
		"todo",
	} {
		t.Run(to, func(t *testing.T) {
			in := base()
			in.From, in.To = auditmode.StatusFiled, to
			in.ActorLevel = LevelL3
			in.ActorIsAdmin = true
			d := Decide(in)
			if d.Allowed {
				t.Fatalf("filed -> %s allowed; a correction must be a new version", to)
			}
			if d.Code != DenyFiled {
				t.Errorf("code = %q, want %q", d.Code, DenyFiled)
			}
		})
	}
}

// Custom-to-built-in is the escape hatch that matters most: without this rule a
// workpaper in review walks out of the chain by being marked done.
func TestAWorkpaperCannotLeaveTheChainForAnOrdinaryStatus(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{auditmode.StatusReviewL2, "done"},
		{auditmode.StatusReviewL1, "todo"},
		{auditmode.StatusDrafting, "done"},
	} {
		t.Run(tc.from+"_to_"+tc.to, func(t *testing.T) {
			in := base()
			in.From, in.To = tc.from, tc.to
			in.ActorLevel = LevelL3
			if d := Decide(in); d.Allowed {
				t.Fatalf("%s -> %s allowed; a workpaper must stay in the chain", tc.from, tc.to)
			}
		})
	}
}

func TestCancellingAWorkpaperNeedsAReviewerRoleOrAdmin(t *testing.T) {
	t.Run("reviewer may", func(t *testing.T) {
		in := base()
		in.From, in.To, in.ActorLevel = auditmode.StatusReviewL2, "cancelled", LevelL1
		if d := Decide(in); !d.Allowed {
			t.Fatalf("a reviewer could not cancel: %s", d.Reason)
		}
	})
	t.Run("admin may", func(t *testing.T) {
		in := base()
		in.From, in.To, in.ActorLevel = auditmode.StatusDrafting, "cancelled", ""
		in.ActorIsAdmin = true
		if d := Decide(in); !d.Allowed {
			t.Fatalf("an admin could not cancel: %s", d.Reason)
		}
	})
	t.Run("the preparer may not, whatever rank they hold", func(t *testing.T) {
		// Cancelling disposes of the work. Leaving that with the person who did
		// it lets an inconvenient workpaper be taken out of the chain by the one
		// party the chain exists to check — the same hole as self-review,
		// through a different door.
		in := base()
		in.From, in.To, in.ActorLevel = auditmode.StatusReviewL2, "cancelled", LevelL1
		in.ActorMemberID, in.ActorIsAdmin = preparer, true
		d := Decide(in)
		if d.Allowed {
			t.Fatal("the preparer cancelled their own workpaper")
		}
		if d.Code != DenySelfReview {
			t.Errorf("code = %q, want %q", d.Code, DenySelfReview)
		}
	})

	t.Run("plain member may not", func(t *testing.T) {
		in := base()
		in.From, in.To, in.ActorLevel = auditmode.StatusReviewL2, "cancelled", ""
		if d := Decide(in); d.Allowed {
			t.Fatal("a member with no role cancelled a workpaper; cancellation must not be an unguarded exit from the chain")
		}
	})
}

// An agent inherits its runtime owner's credentials, so every review decision
// has to be closed to it explicitly rather than by hoping it holds no role.
func TestAnAgentCannotMakeAReviewDecision(t *testing.T) {
	for _, tc := range []struct {
		from  string
		to    string
		level Level
	}{
		{auditmode.StatusReviewL1, auditmode.StatusReviewL2, LevelL1},
		{auditmode.StatusReviewL3, auditmode.StatusFiled, LevelL3},
		{auditmode.StatusDrafting, auditmode.StatusReviewL1, ""},
		{auditmode.StatusReviewL2, "cancelled", LevelL2},
	} {
		in := base()
		in.From, in.To, in.ActorLevel = tc.from, tc.to, tc.level
		in.ActorIsAgent = true
		in.ActorMemberID = ""
		if d := Decide(in); d.Allowed {
			t.Errorf("an agent performed %s -> %s", tc.from, tc.to)
		}
	}
}

func TestCreatingAWorkpaperCannotStartInsideTheChain(t *testing.T) {
	for _, to := range []string{
		auditmode.StatusReviewL1,
		auditmode.StatusReviewL3,
		auditmode.StatusFiled,
	} {
		t.Run(to, func(t *testing.T) {
			in := base()
			in.From, in.To = "", to
			in.ActorLevel, in.ActorIsAdmin = LevelL3, true
			if d := Decide(in); d.Allowed {
				t.Fatalf("an issue was created directly at %s; a fabricated filed workpaper is the fraud this control exists to stop", to)
			}
		})
	}
}

func TestCreatingAWorkpaperInDraftingOrAnOrdinaryStatusIsFine(t *testing.T) {
	for _, to := range []string{auditmode.StatusDrafting, "todo", "backlog"} {
		t.Run(to, func(t *testing.T) {
			in := base()
			in.From, in.To, in.ActorLevel, in.PreparerID = "", to, "", ""
			if d := Decide(in); !d.Allowed {
				t.Fatalf("creating at %s denied: %s", to, d.Reason)
			}
		})
	}
}

// A request that edits a workpaper without touching its status is not a
// transition. It must not be read as one — reporting "cannot move from
// review_l1 to \"\"" for a description edit would be nonsense, and refusing it
// would freeze a workpaper the moment it entered review.
func TestEditingAWorkpaperWithoutChangingStatusIsNotATransition(t *testing.T) {
	for _, from := range []string{
		auditmode.StatusDrafting,
		auditmode.StatusReviewL1,
		auditmode.StatusReviewL3,
	} {
		t.Run(from, func(t *testing.T) {
			in := base()
			in.From, in.To, in.ActorLevel = from, "", ""
			if d := Decide(in); !d.Allowed {
				t.Fatalf("a content edit at %s was denied: %s", from, d.Reason)
			}
		})
	}
}

// The one exception: filed means filed. A content edit is still a write.
func TestAFiledWorkpaperRejectsEvenAContentEdit(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorIsAdmin = auditmode.StatusFiled, "", true
	d := Decide(in)
	if d.Allowed {
		t.Fatal("a filed workpaper accepted a content edit")
	}
	if d.Code != DenyFiled {
		t.Errorf("code = %q, want %q", d.Code, DenyFiled)
	}
}

// Governs is the caller's zero-cost filter. If it ever said false for a
// transition Decide would deny, the gate would be silently skipped.
func TestGovernsCoversEveryTransitionDecideCanDeny(t *testing.T) {
	chain := []string{
		auditmode.StatusDrafting,
		auditmode.StatusReviewL1,
		auditmode.StatusReviewL2,
		auditmode.StatusReviewL3,
		auditmode.StatusFiled,
	}
	ordinary := []string{"", "backlog", "todo", "in_progress", "done", "blocked", "cancelled"}
	all := append(append([]string{}, chain...), ordinary...)

	for _, from := range all {
		for _, to := range all {
			in := base()
			in.From, in.To = from, to
			in.ActorLevel, in.ActorIsAdmin, in.PreparerID = "", false, ""
			if d := Decide(in); !d.Allowed && !Governs(from, to) {
				t.Errorf("Decide denies %q -> %q but Governs says it needs no check", from, to)
			}
		}
	}
}

func TestGovernsIsFalseForOrdinaryWork(t *testing.T) {
	if Governs("todo", "done") {
		t.Error("Governs(todo, done) = true; ordinary work must cost nothing")
	}
	if Governs("in_progress", "") {
		t.Error("Governs(in_progress, \"\") = true; a plain content edit must cost nothing")
	}
}

func TestEveryDenialCarriesAReasonAndACode(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusFiled, auditmode.StatusDrafting
	d := Decide(in)
	if d.Allowed {
		t.Fatal("expected a denial to inspect")
	}
	if strings.TrimSpace(d.Reason) == "" {
		t.Error("denial has no reason; the message is what the UI shows the auditor")
	}
	if d.Code == "" {
		t.Error("denial has no code")
	}
}
