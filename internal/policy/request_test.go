package policy

import "testing"

func TestThePageHeadingReachesJevsState(t *testing.T) {
	o := shop()
	o.Heading = "Contact"
	prompt := BuildPrompt(o, BuildCandidates(o, nil), "Open the contact page", nil, "m")
	if got := prompt.Request.State.(State).Page.Heading; got != "Contact" {
		t.Fatalf("heading = %q", got)
	}
}
