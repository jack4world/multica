package auditmeta

import "testing"

// The boundary rules for an auditee's and an engagement's recorded facts.
// Pure: what is storable and what is refused, with no database.

func TestAPeriodMayBeOpenAtEitherEnd(t *testing.T) {
	// An engagement is often opened before its scope is fixed.
	for _, tc := range []struct{ name, start, end string }{
		{"both", "2025-01-01", "2025-12-31"},
		{"neither", "", ""},
		{"start only", "2025-01-01", ""},
		{"end only", "", "2025-12-31"},
		{"same day", "2025-06-30", "2025-06-30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePeriod(tc.start, tc.end); err != nil {
				t.Errorf("period %q..%q refused: %v", tc.start, tc.end, err)
			}
		})
	}
}

func TestAPeriodCannotEndBeforeItStarts(t *testing.T) {
	if err := ValidatePeriod("2025-12-31", "2025-01-01"); err == nil {
		t.Error("an impossible period was accepted")
	}
}

func TestAPeriodMustBeCalendarDates(t *testing.T) {
	for _, bad := range []string{"2025-13-01", "01/01/2025", "yesterday", "2025"} {
		if err := ValidatePeriod(bad, ""); err == nil {
			t.Errorf("accepted %q as a date", bad)
		}
	}
}

// A closed list is what lets engagements be counted by kind, which is the only
// reason to record the type. Free text would produce five spellings of the
// same audit.
func TestTheAuditTypeListIsClosed(t *testing.T) {
	for _, ok := range AuditTypes() {
		if !ValidAuditType(ok) {
			t.Errorf("%q is in the list but rejected", ok)
		}
	}
	for _, bad := range []string{"离任审计", "Exit Audit", "", "  ", "custom"} {
		if ValidAuditType(bad) {
			t.Errorf("accepted %q as an audit type", bad)
		}
	}
}

// The unusual engagement must be recordable, or people will pick the nearest
// wrong category and the counts stop meaning anything.
func TestTheListCarriesAnEscape(t *testing.T) {
	if !ValidAuditType(AuditTypeOther) {
		t.Fatal("no escape in the audit type list")
	}
}

func TestAnAuditTypeIsOptional(t *testing.T) {
	if !ValidAuditTypeOrEmpty("") {
		t.Error("an engagement must be openable before its kind is decided")
	}
}

// The label is a MARKING, not a permission. The set is closed so the interface
// can render each one distinctly; nothing else reads it.
func TestTheConfidentialityLabelSetIsClosed(t *testing.T) {
	for _, ok := range ConfidentialityLabels() {
		if !ValidConfidentiality(ok) {
			t.Errorf("%q is in the set but rejected", ok)
		}
	}
	for _, bad := range []string{"top_secret", "1", "机密", "  "} {
		if ValidConfidentiality(bad) {
			t.Errorf("accepted %q as a confidentiality label", bad)
		}
	}
	if !ValidConfidentialityOrEmpty("") {
		t.Error("an auditee must be creatable before its sensitivity is decided")
	}
}
