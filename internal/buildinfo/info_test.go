package buildinfo

import "testing"

func TestBuildIdentityNeverGuessesStableVersionFromBranch(t *testing.T) {
	version, revision := Version, Revision
	t.Cleanup(func() { Version, Revision = version, revision })
	Revision = "0123456789012345678901234567890123456789"
	for _, requested := range []string{"main", "development", "source", "1.0.0", "1.0.5", "v1.0.5", "1.0.6", "v1.0.6", "1.0.7", "v1.0.7", "1.0.8", "v1.0.8", "1.0.9", "v1.0.9", "v1.3.6", "v1.3.6/untrusted"} {
		Version = requested
		got := Current()
		if got.Version != requested || got.Revision != Revision {
			t.Fatal(got)
		}
		hasNotes := false
		switch requested {
		case "1.0.5", "v1.0.5", "1.0.6", "v1.0.6", "1.0.7", "v1.0.7", "1.0.8", "v1.0.8", "1.0.9", "v1.0.9":
			hasNotes = true
		}
		if (len(got.ReleaseNotes) == 3) != hasNotes {
			t.Fatal("notes do not match installed version", got)
		}
		if (got.ReleaseURL != "") != (hasNotes || requested == "1.0.0" || requested == "v1.3.6") {
			t.Fatal("invalid stable link", got)
		}
	}
}

func TestStableReleaseOrdering(t *testing.T) {
	for _, row := range []struct {
		candidate, current string
		older              bool
	}{
		{"1.0.5", "1.0.6", true},
		{"v1.9.9", "1.10.0", true},
		{"1.0.10", "v1.0.9", false},
		{"1.0.5", "v1.0.5", false},
		{"1.0.5", "development", false},
		{"1.0.5-rc1", "1.0.6", false},
	} {
		if got := IsOlderStableRelease(row.candidate, row.current); got != row.older {
			t.Errorf("%q older than %q = %v, want %v", row.candidate, row.current, got, row.older)
		}
	}
}
