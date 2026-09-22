package policy

import (
	"fmt"
	"regexp"
	"strings"
)

var quoted = regexp.MustCompile(`(?:^|[\s(:])(['"“‘])([^'"“”‘’\n]{1,80})(['"”’])(?:$|[\s.,;:!?)])`)

// QuotedSpans returns the quoted phrases in a goal, in order and without duplicates. Apostrophes inside words do not count.
func QuotedSpans(goal string) []string {
	seen := map[string]bool{}
	var spans []string
	// Matches may share a boundary character, so scan on from each match's closing quote rather than its end.
	for start := 0; start < len(goal); {
		loc := quoted.FindStringSubmatchIndex(goal[start:])
		if loc == nil {
			break
		}
		span := strings.TrimSpace(goal[start+loc[4] : start+loc[5]])
		if span != "" && !seen[span] {
			seen[span] = true
			spans = append(spans, span)
		}
		start += loc[7]
	}
	return spans
}

// ValueState is what Jev reads when picking a field's text.
type ValueState struct {
	Goal          string        `json:"goal"`
	Field         string        `json:"field"`
	RecentActions []RecentState `json:"recent_actions"`
}

// ValuePrompt asks which quoted phrase from the goal belongs in a field, following TypeSafe's pre-parsed value pattern.
type ValuePrompt struct {
	Request Request
}

func BuildValuePrompt(goal string, field Target, spans []string, history []Action, model string) ValuePrompt {
	criteria := &Ordered{}
	for i, s := range spans {
		criteria.Set(fmt.Sprintf("v%d", i+1), s)
	}
	criteria.Set("NONE", "None of these texts belongs in this field.")
	recent := []RecentState{}
	for _, a := range tail(history, 4) {
		recent = append(recent, RecentState{Op: a.Op, Label: a.Label})
	}
	return ValuePrompt{Request: Request{
		Model: model,
		State: ValueState{Goal: goal, Field: describeTarget(field), RecentActions: recent},
		Questions: map[string]Question{
			"value": choiceQuestion(goalQuestion{Goal: goal, Question: "Which exact text from the goal should be typed into `field`?"}, criteria),
		},
	}}
}

// Read returns the phrase Jev picked, or false when it chose none or the vote was not clear.
func (v ValuePrompt) Read(resp Response, g Gates) (string, bool, error) {
	q := v.Request.Questions["value"]
	r, err := rank(resp, "value", q)
	if err != nil {
		return "", false, err
	}
	if r.Choice == "NONE" || !g.clear(r) {
		return "", false, nil
	}
	text, _ := q.Choices.Get(r.Choice)
	return text, true, nil
}
