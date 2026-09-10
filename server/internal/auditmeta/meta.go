// Package auditmeta holds the boundary rules for the facts an auditee and an
// engagement record about themselves.
//
// Two of them describe the ENTITY under audit and live on the workspace,
// because they outlive any single audit of it: re-entering a client's name for
// every year's audit is how copies drift apart. Two describe ONE audit and live
// on the project.
//
// There is deliberately no sensitivity label here. ADR-0001 makes auditee
// membership the whole isolation model, and a field that restates that without
// enforcing it invites the opposite reading — somebody relies on a badge marked
// 绝密 as protection. Material that must be kept separate goes in a separate
// auditee.
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
