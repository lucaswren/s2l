package buildinfo

import "testing"

func TestBuildIdentityNeverGuessesStableVersionFromBranch(t *testing.T) {
	version, revision := Version, Revision
	t.Cleanup(func() { Version, Revision = version, revision })
	Revision = "0123456789012345678901234567890123456789"
	for _, requested := range []string{"main", "development", "source", "v1.3.6", "v1.3.6/untrusted"} {
		Version = requested
		got := Current()
		if got.Version != requested || got.Revision != Revision || len(got.ReleaseNotes) != 3 {
			t.Fatal(got)
		}
		if (got.ReleaseURL != "") != (requested == "v1.3.6") {
			t.Fatal("invalid stable link", got)
		}
	}
}
