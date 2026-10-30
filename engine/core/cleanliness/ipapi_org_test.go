package cleanliness

import (
	"encoding/json"
	"testing"
)

// The regression: ipapi.is answers with "company": "" for an address with no
// company on record. Declared as a struct, that made the whole response fail
// to decode and the probe returned nothing every 20 seconds.
func TestIPApiOrgAcceptsNonObject(t *testing.T) {
	for _, raw := range []string{`""`, `null`, `"unknown"`, `0`, `[]`} {
		var o ipapiOrg
		if err := json.Unmarshal([]byte(raw), &o); err != nil {
			t.Errorf("company %s: unexpected error %v", raw, err)
			continue
		}
		if o != (ipapiOrg{}) {
			t.Errorf("company %s: got %+v, want zero value", raw, o)
		}
	}
}

func TestIPApiOrgReadsObject(t *testing.T) {
	var o ipapiOrg
	raw := `{"type":"hosting","country":"nl","abuser_score":"0.0586 (High)"}`
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if o.Type != "hosting" || o.Country != "nl" || o.AbuserScore != "0.0586 (High)" {
		t.Fatalf("got %+v", o)
	}
}

// abuser_score is documented as a string but comes back as a number for some
// addresses. Either shape must decode.
func TestIPApiOrgAbuserScoreAsNumber(t *testing.T) {
	var o ipapiOrg
	if err := json.Unmarshal([]byte(`{"type":"isp","abuser_score":0.0586}`), &o); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if o.AbuserScore != "0.0586" {
		t.Fatalf("abuser_score: got %q, want %q", o.AbuserScore, "0.0586")
	}
}

func TestIPApiOrgMissingFields(t *testing.T) {
	var o ipapiOrg
	if err := json.Unmarshal([]byte(`{}`), &o); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if o != (ipapiOrg{}) {
		t.Fatalf("got %+v, want zero value", o)
	}
}

// The flags the UI shows must survive a response whose company block is a
// string — that is the whole point of decoding leniently.
func TestIPApiIsResponseDecodesWithStringCompany(t *testing.T) {
	body := []byte(`{
		"is_datacenter": true,
		"is_vpn": true,
		"company": "",
		"asn": {"type":"hosting","country":"nl","abuser_score":"0.05 (High)"},
		"abuse": {"email":"abuse@example.com"}
	}`)
	var j struct {
		IsDatacenter bool     `json:"is_datacenter"`
		IsVPN        bool     `json:"is_vpn"`
		Company      ipapiOrg `json:"company"`
		Asn          ipapiOrg `json:"asn"`
		Abuse        struct {
			Email string `json:"email"`
		} `json:"abuse"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if !j.IsDatacenter || !j.IsVPN {
		t.Error("flags lost")
	}
	if j.Asn.Type != "hosting" || j.Abuse.Email != "abuse@example.com" {
		t.Errorf("asn/abuse lost: %+v %q", j.Asn, j.Abuse.Email)
	}
}

func TestJSONScalarToString(t *testing.T) {
	cases := map[string]string{
		`"x"`:   "x",
		`""`:    "",
		`null`:  "",
		`1`:     "1",
		`0.125`: "0.125",
		`{}`:    "",
		`[]`:    "",
		``:      "",
	}
	for raw, want := range cases {
		if got := jsonScalarToString(json.RawMessage(raw)); got != want {
			t.Errorf("%s: got %q, want %q", raw, got, want)
		}
	}
}
