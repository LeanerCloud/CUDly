# Agent-SEO README — Claims vs. Current Implementation

## TL;DR — Eli & Cristi to work through together

Eli and Cristi talked through the Sonnet 5 / Opus 5 test results and they align well with where Cristi is independently headed: he's looking at splitting the project into three repos (core library, the web UI with continuous purchasing, and a CLI that will pick up some variant of "safe-reserved-instance" naming). The current repo gets renamed into the new CLI repo to keep its SEO and stars; the other two pieces move out. **Once that split lands, this README needs another pass** — right now it still documents the web dashboard and the MCP server in full, both of which are headed to other repos. Not touched in this round since the split isn't final yet, but flagging it here so it doesn't get lost.

For the purchase-safety mechanism itself, the design changed based on the test: instead of a plan/approval state machine (submit → pending → separately approved), `--purchase` now does what it always said it did — it purchases — but is gated on a **real, interactive terminal**, sudo-style. A non-interactive caller (a script, a CI job, an AI agent driving the CLI as a subprocess) can't get a purchase through no matter what flags it passes; CUDly refuses outright and prints the exact command for a human to run themselves. This is simpler than the plan/approval model, avoids the internal contradictions that model kept surfacing throughout the doc (see "Resolved" below), and lines up with what the test actually showed: a careful agent won't execute an irreversible multi-year commitment autonomously regardless of how the tool markets its own safety, so the honest job for the CLI is to make a human's approval structurally required, not just to ask nicely and hope the agent stops.

This file tracks every claim in the README/`docs/cli/purchase-safety.md` changes that describes behavior CUDly doesn't have yet. The copy is written as the target state we're committing to build; this is the honest build list that has to close before any of it goes public. Nothing here should stay open when this ships.

## Still open — needs code

1. **The interactive-terminal gate itself.** Today, `--purchase` (+ the now-removed `--yes`) executes real purchases directly from any invocation, interactive or not — there is no TTY check. Needs: (a) detecting whether the process has a genuine controlling terminal, not just non-empty stdin; (b) reading the confirmation from the terminal device directly (e.g. `/dev/tty` on Unix) rather than stdin, so piped input can't satisfy it; (c) when there's no controlling terminal, refusing immediately and printing the exact ready-to-run command instead of prompting.

2. **Retiring `--yes`'s current behavior.** The flag exists today and skips the confirmation prompt in automation — exactly the bypass we're now removing. Needs an explicit decision on whether to delete the flag outright or repurpose it for something that isn't purchase confirmation, so old scripts fail loudly rather than silently losing their safety net.

3. **Duplicate-purchase prevention on the CLI.** Pre-existing, not introduced by this round: `docs/cli/purchase-safety.md` documents that `--idempotency-window` is accepted but has no effect in the CLI path — dedup only runs in the server-side scheduler. The README's Safety Features section still claims this works, so it needs fixing (or the CLI needs to actually implement it) before either doc goes live.

4. **`--max-instances` conservative default.** The safety framing implies purchases are capped conservatively out of the box; today the flag defaults to `0` (unlimited). No behavior change proposed here — just flagging the gap between "safe by default" framing and the actual default.

5. **"Conservative-by-Default Sizing" vs. actual flag defaults.** Still open from the last round: the feature bullet implies a run starts small; the real defaults are 3-year term, no-upfront payment, 80% coverage — the longest lock-in at the highest coverage level. Either change the defaults or stop calling them conservative.

6. **AWS "Production" status glosses over per-service granularity.** Still open: the Implementation Status table marks AWS "Production," but the AWS CLI Support Matrix further down marks EC2 (Reserved Instances) and Savings Plans — plausibly the largest spend categories — as "Experimental (seeking testers)." A reader who only sees the top-level table gets a rosier picture than the detail supports.

7. **Unrelated pre-existing bug, surfaced incidentally:** the Go version badge says `1.25+`, but the Development section's Prerequisites say `Go 1.26.6 or later`. Doc drift, nothing to do with this project, flagging since it was caught in the same pass.

8. **Pending the repo split:** once Cristi's three-repo restructure lands, this README needs a pass to drop or relocate the Web Interface and MCP Server sections (both describing pieces moving to other repos) and to reflect whatever the CLI's new name ends up being.

## Resolved this round — superseded by the TTY-gate redesign

- **Plan/approval state machine, 4-eyes wiring, and agent-directed refusal messaging** — all part of the earlier draft's design, all replaced by the simpler interactive-terminal gate above. No longer claims requiring a `pending`/`approved`/`rejected` state or wiring into the web dashboard's approval flow; the CLI's own terminal check is now the whole mechanism.
- **Internal contradiction between "plans not purchases" and the Disclaimer / Example 4 / Duplicate Purchase Prevention wording** — resolved by dropping "plan" terminology. `--purchase` uniformly means "this executes a purchase" again throughout the document, gated on the terminal check rather than on an approval state.
- **"The CLI approval mechanism is asserted, never described"** — the mechanism is now described concretely (terminal-device read, not stdin; refusal + printed command when non-interactive) rather than asserted. Still needs building (see item 1 above), but the doc no longer hand-waves it.
- **"Agent-directed refusal" bullet reading as unverifiable rhetoric** — dropped. The new framing doesn't ask an agent to behave a certain way; it makes the bypass structurally unavailable regardless of how the agent behaves.
- **SECURITY.md changes** — reverted at Eli's request. The existing SECURITY.md (full incident-response plan, contact via "see repository settings") is untouched; not part of this PR.
