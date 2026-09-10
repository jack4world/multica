package auditdocs

import "testing"

// The classification tree's path rules. Pure: what a path may look like, and
// what "everything under here" means. No database.

func TestAValidPathIsSegmentsOfDigits(t *testing.T) {
	for _, ok := range []string{"01", "02", "02/01", "02/01/03", "10/20/30/40"} {
		if err := ValidatePath(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
}

func TestAPathIsRefusedWhenItCannotBeFiledUnder(t *testing.T) {
	for _, bad := range []string{
		"",       // nothing to file under
		"/",      // no segment
		"02/",    // trailing separator
		"/02",    // leading separator
		"02//01", // empty segment
		"02/01/", // trailing again
		"ab",     // segments are numeric so they sort
		"02/ab",  // ditto, nested
		"0",      // single digit: two-digit segments keep the tree sorting
		"021",    // three digits: same reason
		"02 /01", // whitespace
	} {
		if err := ValidatePath(bad); err == nil {
			t.Errorf("accepted %q as a category path", bad)
		}
	}
}

// A path is a filing scheme, not a filesystem. An uncapped one makes prefix
// reads slow and paths unreadable.
func TestDepthIsCapped(t *testing.T) {
	if err := ValidatePath("01/02/03/04"); err != nil {
		t.Errorf("four levels refused: %v", err)
	}
	if err := ValidatePath("01/02/03/04/05"); err == nil {
		t.Error("five levels accepted; the depth cap is not doing anything")
	}
}

// THE bug this design is most likely to ship with, and it is invisible until a
// client has enough categories to collide: a prefix query for 02 must return
// 02/01 and must NOT return 020.
func TestAPrefixMatchesChildrenAndNotSiblingsThatStartTheSameWay(t *testing.T) {
	under := UnderPath("02")
	for _, yes := range []string{"02", "02/01", "02/01/03"} {
		if !under(yes) {
			t.Errorf("%q is not under 02, but it is", yes)
		}
	}
	for _, no := range []string{"020", "020/01", "03", "01/02"} {
		if under(no) {
			t.Errorf("%q counted as under 02; a sibling that starts the same way is not a child", no)
		}
	}
}

// The SQL half of the same rule. A LIKE pattern that omits the separator is the
// form this bug takes in the database.
func TestThePrefixPatternCarriesTheSeparator(t *testing.T) {
	got := DescendantPattern("02")
	if got != "02/%" {
		t.Errorf("pattern = %q, want %q — without the separator it also matches 020", got, "02/%")
	}
}

func TestParentOfAPath(t *testing.T) {
	for _, tc := range []struct{ path, parent string }{
		{"02", ""},
		{"02/01", "02"},
		{"02/01/03", "02/01"},
	} {
		if got := ParentPath(tc.path); got != tc.parent {
			t.Errorf("ParentPath(%q) = %q, want %q", tc.path, got, tc.parent)
		}
	}
}

func TestDepthOfAPath(t *testing.T) {
	for _, tc := range []struct {
		path  string
		depth int
	}{{"02", 1}, {"02/01", 2}, {"02/01/03", 3}} {
		if got := Depth(tc.path); got != tc.depth {
			t.Errorf("Depth(%q) = %d, want %d", tc.path, got, tc.depth)
		}
	}
}

// The standard scheme has to be the same in every auditee, or someone moving
// between clients has to relearn where things live.
func TestTheStandardSchemeIsWellFormedAndNamedInBothLocales(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range StandardScheme() {
		if err := ValidatePath(c.Path); err != nil {
			t.Errorf("seeded path %q is invalid: %v", c.Path, err)
		}
		if seen[c.Path] {
			t.Errorf("duplicate seeded path %q", c.Path)
		}
		seen[c.Path] = true
		if c.Names["zh-Hans"] == "" || c.Names["en"] == "" {
			t.Errorf("seeded category %q is missing a name in one locale", c.Path)
		}
	}
	// Every child's parent is seeded too, or the tree has a hole in the middle.
	for _, c := range StandardScheme() {
		if parent := ParentPath(c.Path); parent != "" && !seen[parent] {
			t.Errorf("seeded %q but not its parent %q", c.Path, parent)
		}
	}
}

// Seeded in an order a caller can apply directly: parents before children, or
// an insert that checks its parent exists fails on the first child.
func TestTheStandardSchemeListsParentsBeforeChildren(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range StandardScheme() {
		if parent := ParentPath(c.Path); parent != "" && !seen[parent] {
			t.Fatalf("%q comes before its parent %q", c.Path, parent)
		}
		seen[c.Path] = true
	}
}
