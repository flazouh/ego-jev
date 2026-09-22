package textgen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/flazouh/ego-jev/internal/policy"
)

func TestParseValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
		err  bool
	}{
		{`{"text": "blue mug"}`, "blue mug", true, false},
		{"```json\n{\"text\": \"Zurich\"}\n```", "Zurich", true, false},
		{`{"text": null}`, "", false, false},
		{`{"text": "  "}`, "", false, false},
		{`[{"role": "searchbox"}]`, "", false, true},
	}
	for _, tc := range cases {
		got, ok, err := ParseValue(tc.in)
		if got != tc.want || ok != tc.ok || (err != nil) != tc.err {
			t.Errorf("ParseValue(%q) = %q %v %v", tc.in, got, ok, err)
		}
	}
}

func TestTextReadsTheFirstChoice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"text\":\"Zurich\"}"}}]}`))
	}))
	defer srv.Close()
	o := NewOpenRouter("k")
	o.Endpoint = srv.URL
	got, ok, err := o.Text(context.Background(), "Find flights from Zurich", policy.Target{Role: "combobox", Label: "Where from?"}, policy.Observation{})
	if err != nil || !ok || got != "Zurich" {
		t.Fatalf("got %q %v %v", got, ok, err)
	}
}
