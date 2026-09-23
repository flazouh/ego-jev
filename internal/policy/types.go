// Package policy turns a page observation into one TypeSafe request and reads Jev's answer back into a decision.
// It is pure: no network, no browser.
package policy

// Observation is what the browser bridge reports about the current viewport.
type Observation struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Heading  string    `json:"heading,omitempty"`
	Text     string    `json:"text"`
	Elements []Element `json:"elements"`
	Viewport Viewport  `json:"viewport"`
	Scroll   Scroll    `json:"scroll"`
}

type Element struct {
	ID       string   `json:"id"`
	Role     string   `json:"role"`
	Label    string   `json:"label"`
	Value    string   `json:"value,omitempty"`
	Context  string   `json:"context,omitempty"`
	Href     string   `json:"href,omitempty"`
	Editable bool     `json:"editable,omitempty"`
	Checked  string   `json:"checked,omitempty"`
	Selected string   `json:"selected,omitempty"`
	Expanded string   `json:"expanded,omitempty"`
	Options  []Option `json:"options,omitempty"`
}

type Option struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Selected bool   `json:"selected,omitempty"`
}

type Viewport struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type Scroll struct {
	Up   bool `json:"up"`
	Down bool `json:"down"`
}

// Op is one operation Jev can choose.
type Op string

const (
	Click      Op = "CLICK"
	Type       Op = "TYPE"
	Select     Op = "SELECT"
	PressEnter Op = "PRESS_ENTER"
	ScrollDown Op = "SCROLL_DOWN"
	ScrollUp   Op = "SCROLL_UP"
	Wait       Op = "WAIT"
	Done       Op = "DONE"
	Review     Op = "REVIEW"
	Blocked    Op = "BLOCKED"
)

// Target is the element an operation acts on. Option is set only for SELECT.
type Target struct {
	ID      string  `json:"id"`
	Role    string  `json:"role"`
	Label   string  `json:"label"`
	Context string  `json:"context,omitempty"`
	Option  *Option `json:"option,omitempty"`
}

// Action is one step already taken, as recorded in the run history.
type Action struct {
	Op      Op     `json:"op"`
	Label   string `json:"label,omitempty"`
	Target  string `json:"target,omitempty"`
	Text    string `json:"text,omitempty"`
	Changed *bool  `json:"changed"`
}

// Gates are the probability and margin a vote must clear before the runner acts on it.
type Gates struct {
	MinProbability float64
	MinMargin      float64
}
