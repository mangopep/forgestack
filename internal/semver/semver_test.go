package semver

import "testing"

func TestParseValidVersions(t *testing.T) {
	cases := []string{"1.2.3", "v1.2.3", "0.1.0", "1.0.0-alpha", "1.0.0+build.5", "1.0.0-alpha+001"}
	for _, c := range cases {
		if _, err := Parse(c); err != nil {
			t.Errorf("Parse(%q) failed: %v", c, err)
		}
	}
}

func TestParseRejectsInvalidVersions(t *testing.T) {
	cases := []string{"", "1.2", "1.2.3.4", "v1.2", "latest", "1.02.3", "01.2.3"}
	for _, c := range cases {
		if _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q) should have failed, got no error", c)
		}
	}
}

func TestCompareBreaking(t *testing.T) {
	old, _ := Parse("1.2.3")
	new, _ := Parse("2.0.0")
	if got := Compare(old, new); got != RelationshipBreaking {
		t.Errorf("Compare(1.2.3, 2.0.0) = %v, want breaking", got)
	}
}

func TestCompareCompatible(t *testing.T) {
	old, _ := Parse("1.2.3")
	new, _ := Parse("1.3.0")
	if got := Compare(old, new); got != RelationshipCompatible {
		t.Errorf("Compare(1.2.3, 1.3.0) = %v, want compatible", got)
	}
}

func TestComparePatch(t *testing.T) {
	old, _ := Parse("1.2.3")
	new, _ := Parse("1.2.4")
	if got := Compare(old, new); got != RelationshipPatch {
		t.Errorf("Compare(1.2.3, 1.2.4) = %v, want patch", got)
	}
}

func TestCompareEqual(t *testing.T) {
	old, _ := Parse("1.2.3")
	new, _ := Parse("1.2.3")
	if got := Compare(old, new); got != RelationshipEqual {
		t.Errorf("Compare(1.2.3, 1.2.3) = %v, want equal", got)
	}
}

func TestCompareDowngrade(t *testing.T) {
	old, _ := Parse("2.0.0")
	new, _ := Parse("1.9.9")
	if got := Compare(old, new); got != RelationshipDowngrade {
		t.Errorf("Compare(2.0.0, 1.9.9) = %v, want downgrade", got)
	}

	old2, _ := Parse("1.5.0")
	new2, _ := Parse("1.4.9")
	if got := Compare(old2, new2); got != RelationshipDowngrade {
		t.Errorf("Compare(1.5.0, 1.4.9) = %v, want downgrade (minor regressed)", got)
	}

	old3, _ := Parse("1.0.5")
	new3, _ := Parse("1.0.4")
	if got := Compare(old3, new3); got != RelationshipDowngrade {
		t.Errorf("Compare(1.0.5, 1.0.4) = %v, want downgrade (patch regressed)", got)
	}
}
