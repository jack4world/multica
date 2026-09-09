// Package auditmeta holds the boundary rules for the facts an auditee and an
// engagement record about themselves.
//
// Two of them describe the ENTITY under audit and live on the workspace,
// because they outlive any single audit of it: re-entering a client's name for
// every year's audit is how copies drift apart. Two describe ONE audit and live
// on the project.
//
// The confidentiality label is the one to read carefully: it is a MARKING on
// the material, not a permission. Access isolation is auditee membership and
// nothing else (ADR-0001), and nothing in this codebase may read this value
// into a query. A label that also filtered would be a second, drifting answer
// to "who can see this".
package auditmeta

import (
	"fmt"
	"strings"
	"time"
)

// Audit types. A CLOSED list, because being able to count engagements by kind
// is the only reason to record the kind at all — free text would produce five
// spellings of 离任审计 and a count of nothing.
const (
	AuditTypeSeparationOfOffice = "separation_of_office"
	AuditTypeInternalControl    = "internal_control"
	AuditTypeSpecial            = "special"
	AuditTypeAnnual             = "annual"
	AuditTypeCompliance         = "compliance"
	// AuditTypeOther is the escape. Without one, an unusual engagement gets
	// filed under the nearest wrong category and the counts stop meaning
	// anything; the specifics go in the engagement's own description.
	AuditTypeOther = "other"
)

// AuditTypes lists the recordable kinds, in the order an interface should show
// them.
func AuditTypes() []string {
	return []string{
		AuditTypeSeparationOfOffice,
		AuditTypeInternalControl,
		AuditTypeSpecial,
		AuditTypeAnnual,
		AuditTypeCompliance,
		AuditTypeOther,
	}
}

// ValidAuditType reports whether v names a recordable kind.
func ValidAuditType(v string) bool {
	for _, t := range AuditTypes() {
		if v == t {
			return true
		}
	}
	return false
}

// ValidAuditTypeOrEmpty allows the unset case: an engagement can be opened
// before its kind is decided.
func ValidAuditTypeOrEmpty(v string) bool { return v == "" || ValidAuditType(v) }

// Confidentiality labels. A closed set so an interface can render each one
// distinctly and consistently.
const (
	ConfidentialityNormal     = "normal"
	ConfidentialityRestricted = "restricted"
	ConfidentialitySecret     = "secret"
)

// ConfidentialityLabels lists the markings, least to most sensitive.
func ConfidentialityLabels() []string {
	return []string{ConfidentialityNormal, ConfidentialityRestricted, ConfidentialitySecret}
}

// ValidConfidentiality reports whether v names a marking.
func ValidConfidentiality(v string) bool {
	for _, l := range ConfidentialityLabels() {
		if v == l {
			return true
		}
	}
	return false
}

// ValidConfidentialityOrEmpty allows the unset case.
func ValidConfidentialityOrEmpty(v string) bool { return v == "" || ValidConfidentiality(v) }

// dateLayout is the calendar-date form the API speaks, matching every other
// date field on the platform.
const dateLayout = "2006-01-02"

// ValidatePeriod checks an engagement's audit period.
//
// Both ends are optional — an engagement is often opened before its scope is
// fixed — but an end before a start is a period that cannot exist, and
// recording one would put every workpaper's evidence window in doubt.
func ValidatePeriod(start, end string) error {
	startAt, err := parseOptionalDate(start, "audit_period_start")
	if err != nil {
		return err
	}
	endAt, err := parseOptionalDate(end, "audit_period_end")
	if err != nil {
		return err
	}
	if startAt != nil && endAt != nil && endAt.Before(*startAt) {
		return fmt.Errorf("audit_period_end (%s) is before audit_period_start (%s)", end, start)
	}
	return nil
}

func parseOptionalDate(v, field string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	parsed, err := time.Parse(dateLayout, v)
	if err != nil {
		return nil, fmt.Errorf("%s must be a calendar date as YYYY-MM-DD", field)
	}
	return &parsed, nil
}
