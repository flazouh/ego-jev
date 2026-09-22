# ego-jev

Drive an [ego-browser](https://lite.ego.app/) (Ego Lite) page with [TypeSafe Jev](https://docs.typesafe.ai/introduction) choosing each step.

A coding agent that uses ego-browser normally spends one model turn per click. `ego-jev` hands a whole sub-task to Jev instead:
code lists what can be done on the page, Jev picks one action in a single request, code performs it and looks again.
When the next step is risky or Jev is not sure, the run stops at the current page and the agent takes over.

```
$ ego-jev run --close --url https://en.wikipedia.org/wiki/Main_Page "Search Wikipedia for 'Jevons paradox' and open that article."
step 1  TYPE        p=0.59  "Search Wikipedia" p=1.00  text="Jevons paradox" from goal  jev 861ms  act 1316ms
step 2  CLICK       p=0.81  "Search" p=0.52  jev 280ms  act 845ms
step 3  DONE        p=0.99  jev 380ms
done after 2 steps in 4.2s
page: https://en.wikipedia.org/wiki/Jevons_paradox (Jevons paradox - Wikipedia)
```

## How it works

Each step:

1. **Observe.** A script inside the page lists up to 200 visible, enabled controls (role, label, value, state, and the
   dialog or group they sit in) plus up to 3,000 characters of visible text.
2. **Hold back risky controls.** Controls whose label or link reads like delete, send, pay, post, submit, and similar are
   removed before Jev sees the options. They are listed as `held_for_user`, and a separate yes/no question asks whether
   the goal needs one of them. A yes (p ≥ 0.5) stops the run as `review` on its own.
3. **Ask Jev once.** One request carries the "which operation" question, one "which target" question per operation
   (speculative fan-out), a yes/no "is the goal met" question, and the held-control question.
4. **Gate.** The operation vote needs p ≥ 0.5 and a 0.1 lead over the runner-up. A text field needs the same. A click or
   dropdown target needs p ≥ 0.4, because every remaining target is a safe move. DONE needs p ≥ 0.8 and a yes on
   "goal met". Anything weaker stops the run as `unsure`.
5. **Find text to type.** First a `--value` you pass, then a quoted phrase from the goal that Jev picks for the field,
   then an optional small text model through OpenRouter.
6. **Act.** Clicks, wheel scrolls, and typing go through raw CDP input. When the click point would hit another element,
   it falls back to ego's own checked `page.click`. Then it waits until the page stops changing.

ego-browser exposes no CDP port and does not pass stdin to scripts, so `ego-jev` starts `ego-browser nodejs` with an
embedded bridge script that connects back over a Unix socket. All decisions happen in Go; the bridge only reads the page
and sends input.

## Install

```sh
go install github.com/flazouh/ego-jev/cmd/ego-jev@latest
```

Put your keys in the environment or in `$XDG_CONFIG_HOME/ego-jev/.env` (default `~/.config/ego-jev/.env`). The environment
wins over the file:

```sh
TYPESAFE_API_KEY=...      # required, from https://console.typesafe.ai
OPENROUTER_API_KEY=...    # optional, writes field text the goal does not quote
```

Check the setup:

```sh
ego-jev doctor
```

## Use

```sh
ego-jev run [flags] "goal"
```

| Flag | Default | Meaning |
|---|---|---|
| `--space ID` | new space | Resume an existing ego task space. |
| `--name NAME` | `ego-jev` | Name of a new task space. |
| `--page LABEL` | `p1` | Page label inside the task space. |
| `--url URL` | | Navigate before the first step. |
| `--value "label=text"` | | Text for a field whose label contains `label`. Repeatable. |
| `--allow-risky REGEXP` | | Let Jev press a held control whose label or link matches (case-insensitive). Repeatable. |
| `--max-steps N` | `25` | Stop after this many actions. |
| `--min-p P` | `0.5` | Lowest probability an operation or text-field vote may have. |
| `--min-margin M` | `0.1` | Lowest lead over the runner-up an operation or text-field vote may have. |
| `--json` | | Print the full result, including the step trace. |
| `--close` | | Finish the task space when the run ends. It stays open by default. |
| `--quiet` | | Do not print each step. |
| `--model` | `jev-latest` | TypeSafe model. |
| `--text-model` | `deepseek/deepseek-v4.1-flash` | OpenRouter model for field text. |
| `--no-text-model` | | Never call a text model. |
| `--env-file` | see below | Dotenv file with the keys. |
| `--ego-browser` | `ego-browser` | ego-browser executable. |

Exit codes: `0` done, `3` handed back to you (`review`, `unsure`, `blocked`, `stuck`, `needs_text`, `max_steps`),
`1` error, `2` usage.

On a hand-back, the task space stays open at that page. `--json` includes `spaceId`, `page`, the reason, the top votes for
`unsure`, the held controls with a CSS selector for `review`, and the field for `needs_text`.

## Measurements

Measured on one Mac in Europe against `jev-1.13.0`, September 2026. Small samples; treat them as a first check, not a
benchmark.

- One Jev decision: about 230 to 870 ms. Observing the local fixture page: 1 to 2 ms.
- A raw CDP click: 4 to 13 ms. ego's `page.click` in the same session: about 70 ms for the first three, then 800 to
  1,100 ms each.
- The e2e suite (`make e2e-live`), four runs: the 6-case local fixture app passed 5, 5, 4, and 5 of 6. The "scroll to a
  link" case reaches the right page every time but stops as `unsure` because the goal-met check says no. The dropdown
  case failed once; its SELECT vote sits near the gate at p=0.58 to 0.65. The 4 read-only real-site goals (Wikipedia
  search, a GitHub tab, a Hacker News link, both Google Flights city fields) passed 3, 4, 4, and 4 of 4; Google Flights
  failed once.

## Limits

- It reads the top document and open shadow roots, not iframes.
- The risky-control filter matches English words.
- With raw CDP input you do not see ego's agent cursor.
- Jev reads instructions literally and struggles with numbers and dates; see TypeSafe's
  [jaggedness notes](https://docs.typesafe.ai/model-jaggedness/jev-1.13).

## Develop

```sh
make check      # gofmt, go vet, unit tests
make e2e        # scripted chooser through a real ego-browser (Ego Lite must be running)
make e2e-live   # real Jev on the fixture app and on real sites
```

```
cmd/ego-jev        CLI
internal/policy    page observation → one TypeSafe request → gated decision (pure)
internal/runner    the observe, decide, act loop and its stop statuses
internal/browser   Go side of the ego-browser bridge, and the embedded bridge.js
internal/typesafe  TypeSafe API client
internal/textgen   optional OpenRouter text model
internal/config    keys from the environment or a dotenv file
internal/scripted  a scripted stand-in for Jev used by tests
e2e                end-to-end tests through a real ego-browser
```

## Credits

The loop follows the pattern of [browser-use/jev-ultrafast](https://github.com/browser-use/jev-ultrafast): an indexed
element table and speculative target questions in one request. Holding risky actions back in code follows
[Cua's jev-use RFC](https://github.com/trycua/cua). Gating on probability and margin follows
[TipTour](https://github.com/milind-soni/tiptour-macos). Picking typed text from quoted goal phrases follows TypeSafe's
pre-parsed value extraction cookbook.

## License

MIT
