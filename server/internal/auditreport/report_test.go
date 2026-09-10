package auditreport

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/auditgate"
)

// The sign-off chain, whole and on one screen.

func base() Input {
	return Input{From: StatusDrafting, ReviewLevels: 2}
}

func TestTheTopRankOfTheEngagementSigns(t *testing.T) {
	cases := []struct {
		depth int
		want  auditgate.Level
	}{
		{1, auditgate.LevelL1},
		{2, auditgate.LevelL2},
		{3, auditgate.LevelL3},
		// An unset depth is the default one, not no chain at all.
		{0, auditgate.LevelL2},
	}
	for _, c := range cases {
		if got := SigningLevel(c.depth); got != c.want {
			t.Errorf("depth %d signs at %q, want %q — a two-level engagement must not wait for a level it does not have",
				c.depth, got, c.want)
		}
	}
}

func TestAReportIsSubmittedThenIssued(t *testing.T) {
	submit := base()
	submit.To = StatusReviewing
	if d := Decide(submit); !d.Allowed || d.Event != EventSubmitted {
		t.Fatalf("submission refused or unrecorded: %+v", d)
	}

	issue := base()
	issue.From, issue.To, issue.ActorLevel = StatusReviewing, StatusIssued, auditgate.LevelL2
	if d := Decide(issue); !d.Allowed || d.Event != EventIssued {
		t.Fatalf("issuing refused or unrecorded: %+v", d)
	}
}

func TestIssuingNeedsTheSigningRank(t *testing.T) {
	for _, level := range []auditgate.Level{"", auditgate.LevelL1, auditgate.LevelL3} {
		in := base()
		in.From, in.To, in.ActorLevel = StatusReviewing, StatusIssued, level
		d := Decide(in)
		if d.Allowed {
			t.Errorf("a two-level engagement's report was issued at rank %q", level)
		}
		if d.Code != DenyRankRequired {
			t.Errorf("rank %q refused as %q, want %q", level, d.Code, DenyRankRequired)
		}
	}
}

// Workspace admin is an administrative role. Signing the department's report is
// not an administrative act.
func TestAdminIsNotAWayAroundTheSignature(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorIsAdmin = StatusReviewing, StatusIssued, true
	if d := Decide(in); d.Allowed {
		t.Error("a workspace admin with no rank issued the report")
	}
}

// The drafter may sign if they hold the rank. In a five-person department the
// 部门负责人 often writes the report, and requiring a second signature there is
// the invented-reviewer mistake ADR-0003 exists to avoid.
func TestTheDrafterMaySignWhenTheyHoldTheRank(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = StatusReviewing, StatusIssued, auditgate.LevelL2
	if d := Decide(in); !d.Allowed {
		t.Errorf("the signing rank was refused: %s", d.Reason)
	}
}

func TestReturningNeedsAReason(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = StatusReviewing, StatusDrafting, auditgate.LevelL2
	if d := Decide(in); d.Code != DenyReasonRequired {
		t.Errorf("a return with no reason was refused as %q, want %q", d.Code, DenyReasonRequired)
	}
	in.Reason = "审计意见与底稿结论不一致"
	if d := Decide(in); !d.Allowed || d.Event != EventReturned {
		t.Errorf("a return with a reason was refused or unrecorded: %+v", d)
	}
}

func TestAnIssuedReportRefusesEveryWrite(t *testing.T) {
	for _, to := range []string{StatusDrafting, StatusReviewing, StatusIssued, ""} {
		in := base()
		in.From, in.To = StatusIssued, to
		in.ActorLevel, in.ActorIsAdmin, in.Reason = auditgate.LevelL3, true, "改一下"
		d := Decide(in)
		if d.Allowed {
			t.Errorf("an issued report accepted a write to %q", to)
		}
		if d.Code != DenyIssued {
			t.Errorf("write to %q refused as %q, want %q", to, d.Code, DenyIssued)
		}
	}
}

func TestIssuingSkipsNoStep(t *testing.T) {
	in := base()
	in.From, in.To, in.ActorLevel = StatusDrafting, StatusIssued, auditgate.LevelL2
	d := Decide(in)
	if d.Allowed {
		t.Fatal("a report went out without ever being submitted for sign-off")
	}
	if d.Code != DenyIllegalTransition {
		t.Errorf("refused as %q, want %q", d.Code, DenyIllegalTransition)
	}
}

func TestEditingAnUnsignedReportIsNotATransition(t *testing.T) {
	for _, from := range []string{StatusDrafting, StatusReviewing} {
		in := base()
		in.From, in.To = from, ""
		if d := Decide(in); !d.Allowed {
			t.Errorf("editing a report in %s was refused: %s", from, d.Reason)
		}
	}
}

func TestAnAgentMakesNoReportDecision(t *testing.T) {
	cases := []struct{ from, to string }{
		{StatusDrafting, StatusReviewing},
		{StatusReviewing, StatusIssued},
		{StatusReviewing, StatusDrafting},
	}
	for _, c := range cases {
		in := base()
		in.From, in.To = c.from, c.to
		in.ActorIsAgent, in.ActorLevel, in.Reason = true, auditgate.LevelL2, "不行"
		d := Decide(in)
		if d.Allowed {
			t.Errorf("an agent was allowed %s -> %s", c.from, c.to)
		}
		if d.Code != DenyAgent {
			t.Errorf("%s -> %s refused as %q, want %q", c.from, c.to, d.Code, DenyAgent)
		}
	}
}

func TestEveryRefusalCarriesASentenceAndACode(t *testing.T) {
	in := base()
	in.From, in.To = StatusReviewing, StatusIssued
	d := Decide(in)
	if d.Allowed {
		t.Fatal("expected a refusal to inspect")
	}
	if strings.TrimSpace(d.Reason) == "" || d.Code == "" {
		t.Errorf("refusal carries code %q and reason %q", d.Code, d.Reason)
	}
}
