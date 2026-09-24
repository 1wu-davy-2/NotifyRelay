package channel

import (
	"strings"
	"testing"
)

// The overall result has to carry the peer's own words, not just the severity.
//
// A caller reading only the Result is the normal case: the queue records what
// the Result says and nothing else. When every recipient failed, this used to
// leave Detail empty, so a delivery that could not reach its relay at all was
// written down as a class and an error that named neither the address nor the
// reply — which is a failure report that cannot be acted on.
func TestFromRecipients_CarriesThePeerDetail(t *testing.T) {
	res := FromRecipients([]Recipient{
		{Address: "oncall@example.com", Class: ClassPermanent, Detail: "smtp code=550 reason=Mail from must equal authorized user"},
	})

	if res.Class != ClassPermanent {
		t.Fatalf("class = %v, want PERMANENT", res.Class)
	}
	if !strings.Contains(res.Detail, "oncall@example.com") {
		t.Errorf("detail = %q, want it to name the refused address", res.Detail)
	}
	if !strings.Contains(res.Detail, "550") {
		t.Errorf("detail = %q, want the peer's reply code", res.Detail)
	}
}

// With several recipients, "one of these was refused" is not actionable until
// the detail says which one — so the accepted ones must not appear, and the
// refused ones all must.
func TestFromRecipients_NamesOnlyTheRefused(t *testing.T) {
	res := FromRecipients([]Recipient{
		{Address: "a@example.com", Accepted: true, Class: ClassSent, Detail: "delivered to a@example.com"},
		{Address: "b@example.com", Class: ClassTransient, Detail: "smtp code=451 reason=try later"},
		{Address: "c@example.com", Class: ClassPermanent, Detail: "smtp code=550 reason=no such user"},
	})

	if res.Class != ClassPermanent {
		t.Fatalf("class = %v, want the most severe of the three", res.Class)
	}
	if strings.Contains(res.Detail, "a@example.com") {
		t.Errorf("detail = %q, want the accepted recipient left out", res.Detail)
	}
	for _, want := range []string{"b@example.com", "451", "c@example.com", "550"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("detail = %q, want it to contain %q", res.Detail, want)
		}
	}
}

// A channel that classifies a failure without describing it must not have an
// empty field invented for it: the count is the honest summary there.
func TestFromRecipients_NoDetailWhenTheChannelGaveNone(t *testing.T) {
	res := FromRecipients([]Recipient{
		{Address: "a@example.com", Class: ClassConnectError},
	})

	if res.Detail != "" {
		t.Errorf("detail = %q, want empty — the channel said nothing about why", res.Detail)
	}
	if res.Err == nil {
		t.Error("err is nil; a failure with no detail still has to be an error")
	}
}

// The success path is unchanged: it reports how many got through.
func TestFromRecipients_AllAccepted(t *testing.T) {
	res := FromRecipients([]Recipient{
		{Address: "a@example.com", Accepted: true, Class: ClassSent},
		{Address: "b@example.com", Accepted: true, Class: ClassSent},
	})

	if res.Class != ClassSent {
		t.Fatalf("class = %v, want SENT", res.Class)
	}
	if res.Detail != "2/2 recipients accepted" {
		t.Errorf("detail = %q, want the accepted count", res.Detail)
	}
}
