package remediate

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/auditmode"
)

// The ledger's rules, whole and on one screen. Everything a database is needed
// for lives in internal/handler; what is here is the matrix an auditor would
// argue about.

// USER ids, because that is the namespace issue.assignee_id is written in and
// the namespace the gate compares. Named so a reader cannot mistake them for
// member ids — the two crossing is what let the person who owes a fix close
// their own item.
const (
	responsible = "user-responsible"
	auditor     = "user-auditor"
)

// base is an item on the ledger, in progress, being acted on by its own
// responsible person.
func base() Input {
	return Input{
		IsRemediation:     true,
		From:              auditmode.StatusRemediating,
		ActorUserID:       responsible,
		ResponsibleUserID: responsible,
	}
}

func TestTheResponsiblePersonStartsAndSubmits(t *testing.T) {
	start := base()
	start.From, start.To = "todo", auditmode.StatusRemediating
	if d := Decide(start); !d.Allowed || d.Event != EventStarted {
		t.Errorf("starting work refused or unrecorded: %+v", d)
	}

	submit := base()
	submit.To, submit.Note = auditmode.StatusPendingVerification, "补签了三份合同"
	if d := Decide(submit); !d.Allowed || d.Event != EventSubmitted {
		t.Errorf("submitting for verification refused or unrecorded: %+v", d)
	}
}

// The rule the whole ledger rests on. Without it "已关闭" means the person who
// was asked to fix something decided they had.
func TestTheResponsiblePersonCannotVerifyTheirOwnFix(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusPendingVerification, auditmode.StatusRemediationClosed
	in.ActorIsVerifier, in.Note = true, "看过整改材料"
	d := Decide(in)
	if d.Allowed {
		t.Fatal("the person responsible for the fix was allowed to close their own item")
	}
	if d.Code != DenySelfVerification {
		t.Errorf("refused as %q, want %q", d.Code, DenySelfVerification)
	}
}

// Nor by cancelling it, which disposes of the item just as finally.
func TestTheResponsiblePersonCannotCancelTheirOwnItem(t *testing.T) {
	in := base()
	in.To, in.ActorIsVerifier, in.ActorIsAdmin = "cancelled", true, true
	if d := Decide(in); d.Allowed {
		t.Error("the person responsible for the fix was allowed to cancel it")
	}
}

func TestClosingNeedsAVerifierFromTheRaisingEngagement(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusPendingVerification, auditmode.StatusRemediationClosed
	in.ActorUserID, in.Note = "someone-else", "看过了"
	d := Decide(in)
	if d.Allowed {
		t.Fatal("a member with no rank on the raising engagement closed an item")
	}
	if d.Code != DenyVerifierRequired {
		t.Errorf("refused as %q, want %q", d.Code, DenyVerifierRequired)
	}
}

func TestClosingNeedsAnAccountOfWhatWasChecked(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusPendingVerification, auditmode.StatusRemediationClosed
	in.ActorUserID, in.ActorIsVerifier = auditor, true
	for _, note := range []string{"", "   ", "\n\t"} {
		in.Note = note
		d := Decide(in)
		if d.Allowed {
			t.Errorf("closed an item with note %q", note)
		}
		if d.Code != DenyNoteRequired {
			t.Errorf("note %q refused as %q, want %q", note, d.Code, DenyNoteRequired)
		}
	}
}

func TestSendingAFixBackNeedsAReason(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusPendingVerification, auditmode.StatusRemediating
	in.ActorUserID, in.ActorIsVerifier = auditor, true
	if d := Decide(in); d.Code != DenyNoteRequired {
		t.Errorf("a rejection with no reason was refused as %q, want %q", d.Code, DenyNoteRequired)
	}
	in.Note = "整改材料只覆盖了两个月"
	if d := Decide(in); !d.Allowed || d.Event != EventRejected {
		t.Errorf("a rejection with a reason was refused or unrecorded: %+v", d)
	}
}

func TestSubmittingNeedsAnAccountOfWhatWasDone(t *testing.T) {
	in := base()
	in.To = auditmode.StatusPendingVerification
	if d := Decide(in); d.Code != DenyNoteRequired {
		t.Errorf("submitted with no account of the fix, refused as %q", d.Code)
	}
}

// The escape hatch that matters most: 待验证 -> done would close the item with
// nobody verifying anything.
func TestAnItemCannotLeaveTheLedgerForAnOrdinaryDoneStatus(t *testing.T) {
	for _, from := range []string{auditmode.StatusRemediating, auditmode.StatusPendingVerification} {
		in := base()
		in.From, in.To = from, "done"
		in.ActorIsVerifier, in.ActorIsAdmin, in.ActorUserID = true, true, auditor
		d := Decide(in)
		if d.Allowed {
			t.Errorf("%s -> done was allowed; the item left the ledger unverified", from)
		}
		if d.Code != DenyLeavesChain {
			t.Errorf("%s -> done refused as %q, want %q", from, d.Code, DenyLeavesChain)
		}
	}
}

func TestAClosedItemRefusesEveryWrite(t *testing.T) {
	for _, to := range []string{auditmode.StatusRemediating, auditmode.StatusPendingVerification, "cancelled", "todo", ""} {
		in := base()
		in.From, in.To = auditmode.StatusRemediationClosed, to
		in.ActorUserID, in.ActorIsVerifier, in.ActorIsAdmin = auditor, true, true
		in.Note = "改主意了"
		d := Decide(in)
		if d.Allowed {
			t.Errorf("a closed item accepted a write to %q", to)
		}
		if d.Code != DenyClosed {
			t.Errorf("write to %q refused as %q, want %q", to, d.Code, DenyClosed)
		}
	}
}

func TestClosureIsReachedOnlyThroughVerification(t *testing.T) {
	in := base()
	in.From, in.To = auditmode.StatusRemediating, auditmode.StatusRemediationClosed
	in.ActorUserID, in.ActorIsVerifier, in.Note = auditor, true, "看过了"
	d := Decide(in)
	if d.Allowed {
		t.Fatal("an item was closed without ever being submitted for verification")
	}
	if d.Code != DenyIllegalTransition {
		t.Errorf("refused as %q, want %q", d.Code, DenyIllegalTransition)
	}
}

// An ordinary issue put on a remediation status would be an item with no
// department, no deadline and no engagement whose ranks could close it.
func TestAnOrdinaryIssueCannotEnterTheLedger(t *testing.T) {
	in := base()
	in.IsRemediation = false
	in.From, in.To = "todo", auditmode.StatusRemediating
	in.ActorIsAdmin = true
	d := Decide(in)
	if d.Allowed {
		t.Fatal("an ordinary issue entered the remediation chain")
	}
	if d.Code != DenyNotRemediation {
		t.Errorf("refused as %q, want %q", d.Code, DenyNotRemediation)
	}
}

func TestAnAgentMakesNoRemediationDecision(t *testing.T) {
	cases := []struct{ from, to string }{
		{"todo", auditmode.StatusRemediating},
		{auditmode.StatusRemediating, auditmode.StatusPendingVerification},
		{auditmode.StatusPendingVerification, auditmode.StatusRemediationClosed},
		{auditmode.StatusPendingVerification, auditmode.StatusRemediating},
		{auditmode.StatusRemediating, "cancelled"},
	}
	for _, c := range cases {
		in := base()
		in.From, in.To = c.from, c.to
		in.ActorIsAgent, in.ActorUserID, in.ActorIsVerifier = true, "", true
		in.Note = "整改完成"
		d := Decide(in)
		if d.Allowed {
			t.Errorf("an agent was allowed %s -> %s", c.from, c.to)
		}
		if d.Code != DenyAgent {
			t.Errorf("%s -> %s refused as %q, want %q", c.from, c.to, d.Code, DenyAgent)
		}
	}
}

// A content edit is not a transition. Freezing an item the moment it entered
// the chain would make the ledger unusable.
func TestEditingContentIsNotATransition(t *testing.T) {
	for _, from := range []string{auditmode.StatusRemediating, auditmode.StatusPendingVerification} {
		in := base()
		in.From, in.To = from, ""
		if d := Decide(in); !d.Allowed {
			t.Errorf("a content edit at %s was refused: %s", from, d.Reason)
		}
	}
}

// The batch endpoint is all-or-nothing: an item already on the target status
// must not fail the whole request.
func TestResendingTheCurrentStatusIsANoOp(t *testing.T) {
	in := base()
	in.To = in.From
	if d := Decide(in); !d.Allowed {
		t.Errorf("re-sending the current status was refused: %s", d.Reason)
	}
}

func TestAWriteOutsideTheChainIsNotGoverned(t *testing.T) {
	if Governs("todo", "in_progress") {
		t.Error("an ordinary issue write was reported as governed")
	}
	if !Governs("todo", auditmode.StatusRemediating) {
		t.Error("entering the ledger was not reported as governed")
	}
	if !Governs(auditmode.StatusPendingVerification, "done") {
		t.Error("leaving the ledger was not reported as governed")
	}
}

// Every refusal has to say something a person can act on. A code with no
// sentence behind it reaches the reader as a machine identifier.
func TestEveryRefusalCarriesASentence(t *testing.T) {
	in := base()
	for _, to := range []string{auditmode.StatusRemediationClosed, "done", "cancelled", auditmode.StatusPendingVerification} {
		probe := in
		probe.From, probe.To = auditmode.StatusPendingVerification, to
		probe.ActorUserID = responsible
		d := Decide(probe)
		if d.Allowed {
			continue
		}
		if strings.TrimSpace(d.Reason) == "" {
			t.Errorf("refusal of -> %q carries no sentence", to)
		}
		if d.Code == "" {
			t.Errorf("refusal of -> %q carries no code", to)
		}
	}
}
