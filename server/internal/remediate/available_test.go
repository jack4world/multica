package remediate

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/auditmode"
)

// What the interface may offer on a remediation item. Derived from the same
// matrix that enforces the ledger, so the buttons cannot drift from the rules.

func keys(actions []Action) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, a.To)
	}
	return out
}

func TestTheResponsiblePersonIsOfferedSubmission(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusRemediating, ""
	offered := keys(Available(in))
	if !contains(offered, auditmode.StatusPendingVerification) {
		t.Errorf("offered %v to the person doing the work, want submission for verification", offered)
	}
	if contains(offered, auditmode.StatusRemediationClosed) {
		t.Errorf("offered closure to the person doing the work: %v", offered)
	}
}

func TestAVerifierIsOfferedClosureAndReturn(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusPendingVerification, ""
	in.ActorMemberID, in.ActorIsVerifier = auditor, true
	offered := keys(Available(in))
	for _, want := range []string{auditmode.StatusRemediationClosed, auditmode.StatusRemediating} {
		if !contains(offered, want) {
			t.Errorf("offered %v to a verifier, want %q among them", offered, want)
		}
	}
}

// Independence made visible: the responsible person is not shown the buttons,
// not merely refused when they press one.
func TestTheResponsiblePersonIsOfferedNothingAtVerification(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusPendingVerification, ""
	in.ActorIsVerifier, in.ActorIsAdmin = true, true
	if actions := Available(in); len(actions) != 0 {
		t.Errorf("offered %v to the person whose fix is being verified", keys(actions))
	}
}

func TestAClosedItemOffersNothingToAnyone(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusRemediationClosed, ""
	in.ActorMemberID, in.ActorIsVerifier, in.ActorIsAdmin = auditor, true, true
	if actions := Available(in); len(actions) != 0 {
		t.Errorf("a closed item offered %v", keys(actions))
	}
}

func TestAnActionSaysWhetherItNeedsANote(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusPendingVerification, ""
	in.ActorMemberID, in.ActorIsVerifier = auditor, true
	for _, a := range Available(in) {
		wantNote := a.To != "cancelled"
		if a.RequiresNote != wantNote {
			t.Errorf("action to %q: requires_note = %v, want %v", a.To, a.RequiresNote, wantNote)
		}
	}
}

func statusMatrix() []string {
	return []string{
		"todo",
		auditmode.StatusRemediating,
		auditmode.StatusPendingVerification,
		auditmode.StatusRemediationClosed,
	}
}

// THE property. An interface that offers a button the server would refuse is
// worse than no button: it teaches people the control is arbitrary.
func TestEveryOfferedActionWouldBeAccepted(t *testing.T) {
	for _, from := range statusMatrix() {
		for _, actor := range []string{responsible, auditor} {
			for _, verifier := range []bool{false, true} {
				for _, admin := range []bool{false, true} {
					in := base()
					in.From, in.To = from, ""
					in.ActorMemberID, in.ActorIsVerifier, in.ActorIsAdmin = actor, verifier, admin
					for _, a := range Available(in) {
						check := in
						check.To = a.To
						if a.RequiresNote {
							check.Note = "because"
						}
						if d := Decide(check); !d.Allowed {
							t.Errorf("offered %s -> %s (verifier=%v, admin=%v, self=%v) but the gate refuses it: %s",
								from, a.To, verifier, admin, actor == responsible, d.Reason)
						}
					}
				}
			}
		}
	}
}

// The other half: an action the gate WOULD accept has to be offered, or the
// interface strands people who then fall back to the raw API.
func TestEveryAcceptedTransitionIsOffered(t *testing.T) {
	targets := append(statusMatrix(), "cancelled")
	for _, from := range statusMatrix() {
		for _, actor := range []string{responsible, auditor} {
			for _, verifier := range []bool{false, true} {
				for _, admin := range []bool{false, true} {
					in := base()
					in.From, in.To = from, ""
					in.ActorMemberID, in.ActorIsVerifier, in.ActorIsAdmin = actor, verifier, admin
					offered := map[string]bool{}
					for _, a := range Available(in) {
						offered[a.To] = true
					}
					for _, to := range targets {
						if to == from {
							continue
						}
						check := in
						check.To, check.Note = to, "because"
						if d := Decide(check); d.Allowed && d.Event != "" && !offered[to] {
							t.Errorf("the gate accepts %s -> %s (verifier=%v, admin=%v, self=%v) but it is not offered",
								from, to, verifier, admin, actor == responsible)
						}
					}
				}
			}
		}
	}
}

func TestAnAgentIsOfferedNothing(t *testing.T) {
	for _, from := range statusMatrix() {
		in := base()
		in.From, in.To = from, ""
		in.ActorIsAgent, in.ActorMemberID, in.ActorIsVerifier = true, "", true
		if actions := Available(in); len(actions) != 0 {
			t.Errorf("an agent at %s was offered %v", from, keys(actions))
		}
	}
}

func TestNothingIsOfferedOnAnIssueThatIsNotOnTheLedger(t *testing.T) {
	in := base()
	in.IsRemediation = false
	in.From, in.To = "todo", ""
	in.ActorIsAdmin = true
	if actions := Available(in); len(actions) != 0 {
		t.Errorf("offered %v on an issue that is not a remediation item", keys(actions))
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
