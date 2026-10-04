# Open questions — the four the spec says block tickets

Each question: what it is, the evidence from the code, the options as the spec words them, what each costs
**in this repo**, the recommendation and why, what it unblocks. Terse; every claim is `file:line` or a
`spec.md` / issue citation. Numbers the spec already measured are labelled as such; anything I derived is
labelled *derived* and shown with its arithmetic so it can be checked.

**Derived once, used by Q1:** encoding is **155 ms/frame mean** (131–198, `spec.md:472-473`); bytes are
**90 KB mean at 1600×1000, q90** (`spec.md:473`), i.e. ~**56 KB per megapixel**. `VIEWPORTS`
(`spec.md:110-114`) gives 1600×1000 = **1.60 MP**, 834×1112 = **0.93 MP** (58 % of desktop), 390×844 =
**0.33 MP** (21 %) — so a tablet frame derives to ~52 KB and a phone frame to ~19–35 KB (phone widths wrap
text taller, so the upper end is the honest one). Both are *derived* from the desktop measurement; 05's pilot
replaces them with measured bytes per viewport, which is exactly why the pilot exists.

---

## Q1 — How wide should the still matrix be?

**The question as the spec words it** (`spec.md:749-755`): three tiers, and it "is about *offline coverage*
rather than design": **(a)** as planned — desktop everywhere, ~20–25 mobile, ~10 tablet, dark live-only;
**(b)** every *screen* at desktop + tablet + mobile (~+75 captures), "so the viewport switch never shows a
hole on a screen"; **(c)** every *scene* at all three widths (~360 stills, ~35–40 minutes a run, ~3× the
repository weight). "Ticket 05's measured pilot is what this should be decided on, and the plan is costed
at (a)."

**Evidence from the code**

| evidence | where |
| --- | --- |
| The desktop set is ~95–105 frames and "about a third overlays" → **~65 `kind: 'screen'` scenes** | `spec.md:33, 208-266`; `kind: 'screen' \| 'overlay' \| 'feedback'` at `04:12` |
| (b) multiplies *screens*, not scenes: a non-desktop scene must pair with a desktop scene of the same screen | `04:39-40, 70-71` |
| The pilot is **five frames** (desktop, tablet, mobile, one overlay) and its job is bytes/ms per frame plus the WebP-vs-PNG comparison | `05:9-18, 54-59` |
| The ship budget is currently closed on **the uniform 90 KB** figure (~12 MB for ~130 frames) | `spec.md:581-585` |
| `--viewport desktop\|tablet\|mobile` and `--only` / `--group` already exist, so a wider matrix is a flag, not new machinery | `05:45` |
| The switch's honest failure mode already exists: an uncaptured variant is "a **labelled hole with a Live button**" | `spec.md:387, 764-767` |

**Cost, per option** (encode = measured; wall clock and bytes = *derived*)

| | frames | encode only | wall clock | repo weight |
| --- | --- | --- | --- | --- |
| **(a)** ~100 desktop + ~22 mobile + ~10 tablet | **~130** | 130 × 0.155 = **~20 s** | "low minutes" per the spec; the ratio to (b)/(c) is what matters | 100×90 KB + 22×30 + 10×55 ≈ **~10 MB** (uniform 90 KB → the spec's **~12 MB**) |
| **(b)** every screen at 3 widths — ~65 screens × 3, minus (a)'s non-desktop frames | **~205** (spec: "+~75"; my arithmetic says ~+100 if there are ~65 screens) | 205 × 0.155 = **~32 s** (**+12 s**) | **~1.6 × (a)** | 100×90 + 65×55 + 65×30 ≈ **~15 MB** (**+3–4 MB**; a uniform-90 KB guess would say +6.8 MB) |
| **(c)** every scene at all three widths | **~360** (spec) = ~120 scenes × 3 | 360 × 0.155 = **~56 s** (**+36 s**) | **~2.8 × (a)** | area-weighted **~21 MB**; uniform 90 KB **~32 MB** (the spec's "~3×") |

**One inconsistency to hand 05.** `spec.md:753` prices (c) at "~35–40 minutes a run", which is ~6 s/frame —
the *retracted* fixed-6-s-settle model (`spec.md:625` says so explicitly). Under the readiness model the same
run is ~56 s of encoding plus navigation, so the run is bounded by navigation, not encoding. The ratio between
options is stable ((b) ≈ 1.6×, (c) ≈ 2.8× of (a)); the absolute per-frame wall clock is what 05 must report.

**Can the pilot decide it?** Yes for the *cost* half, no for the *coverage* half. Five frames give measured
bytes and ms at each of the three viewports, enough to price (a) vs (b) — and the answer is likely "cheap":
if a tablet frame is really 58 % and a phone frame 21 % of a desktop frame's pixels, (b) costs ~+3 MB and
~+12 s of encoding. The pilot cannot answer *what the reviewer needs to see*, and cannot size (c) at all
without 04's final scene count.

**Recommendation: (a) as planned, with (b) cheap enough to adopt if the pilot's per-viewport bytes confirm the
area ratio — and (c) rejected on value, not cost.**

Reason: **the (c) argument is the overlap, not the bytes.** (c) adds ~35 overlays × 2 extra widths, and every
one of those mobile frames is the *same* full-height sheet (file 1 §4; `03-responsive-mobile.md:38-39`), so (c)
buys ~120 near-duplicate phone images — while (b)'s marginal cost is ~⅓ of an intuitive guess because
non-desktop frames are smaller, not equal. The "no holes on a screen" property is therefore affordable if the
reviewer wants it; storing the same sheet at three widths is not worth paying for at any price.

**What it unblocks:** 04's `viewports` lists (`04:13, 29-40, 68-71`), 05's full run and its matrix report
(`05:45, 60-61`), the board's viewport switch (06/08, `spec.md:379-420`), and 09's committed size
(`spec.md:556-593`).

**If nobody answers:** 04 defaults to (a), the plan's costed option, and the board shows **labelled holes with a
Live button** (`spec.md:387`) for anything not captured — an honest, visible failure mode rather than a wrong
image. The cost also runs the other way: if 04 guesses (b) and the budget gate closes on the uniform 90 KB
figure, 09 has to renegotiate the committed size, and the pilot ends up justifying a different number than the
one it measured.

---

## Q2 — Tablet's floor: move desktop to 1200, or keep 992 and call tablet a wide phone?

**The question as the spec words it** (`spec.md:757-760`): the plan "moves desktop's floor from 992px to 1200px
so tablet gets a tier of its own (designed at 834, spanning 768–1199). That changes what a 1024-wide window
looks like: a drawer instead of a sider. If you would rather keep 992 as the desktop floor and treat tablet as
'phone layout with more room', say so in ticket 03 — it is a smaller design pass, and a tablet still would then
look much like a wide phone."

**Evidence from the code** (the whole of file 1 §1.1–§2; the load-bearing rows)

| evidence | where |
| --- | --- |
| There is exactly **one** responsive prop in the mock, and it deletes navigation below 992 | `web-design/src/app/AppShell.tsx:158-161` (`breakpoint="lg"`, `collapsedWidth={0}`) |
| **36 `Col` sites in 7 files** use `lg` (992) as the desktop rung — the mock's "desktop" begins at 992, not 1200 | file 1 §1.2 |
| antd's ladder already has the exact rung: `xl` = 1200, so the floor move is a rename (`lg`→`xl`), not new machinery. Tablet 768–1199 = `md ∪ lg`; mobile < 768 = `xs ∪ sm` | file 1 §1.2; antd 6.5.1 (`web-design/package.json:19`) |
| One responsive prop is **not** a `Col`, so a `lg`→`xl` sweep would miss it | `web-design/src/components/ServiceMonitor.tsx:221-222` (`column={{lg:3,sm:2,xs:1}}`) |
| **At 834 today `lg` is false**, so Overview's stats, Monitoring's charts and the States grid are all 1-across — today's "tablet" is a stretched phone | file 1 §8.2 |
| The ticket's own acceptance test requires the tablet tier to be discernible: "At 834 the designed two-column layouts actually appear where the design says they do, and nowhere else" | `03-responsive-mobile.md:70-71` |

### What changes at 1024, option by option

| | **Option (i): floor 1200** (the spec's plan) | **Option (ii): keep 992, tablet = wide phone** |
| --- | --- | --- |
| shell at 1024 | sider → **drawer + menu button** | sider stays at 1024; the drawer is still required below 992 (fixing `collapsedWidth={0}` is mandatory either way) |
| Overview | stat row **4→2 across**; the 15/9 row stacks (composes list above activity) | unchanged from today: 4-across stats, 15/9 split |
| Compose | 15/9 → stacked; **the editor "as tall as the rail" contract stops holding** (`GUIDELINES.md:331`) | unchanged: editor beside the rail |
| Monitoring | pairs → 1-across at 1024, and **pairs at 834** under the designed tier | 2-across at 1024; **1-across at 834** = a wide phone |
| Projects | card grid 3→2 across | 3-across |
| Project / States / Guidelines | States 2→1; Guidelines' `md` cards unchanged | unchanged |
| at 834 specifically | 2×2 stats, chart pairs, 2-across cards — the tier is *visible* | everything 1-across; the tablet frame differs from the phone by whitespace |
| cost | 36 `Col` sites + `ServiceMonitor.tsx:221` + a JS width test in the shell (the mock's first `Grid`/`matchMedia` use) + one designed pass | ~0 grid changes; the drawer only — but 03's "two columns at 834" item (`03-responsive-mobile.md:70-71`) becomes unfalsifiable and most of 03's screen list (`:24-52`) is a no-op |

**Recommendation: (i) — move the floor to 1200.** Reason: **1024 today is the worst of both worlds** — the
desktop composition (4-across stats, a 15/9 Compose split, 3-across cards) with the navigation deleted
(`AppShell.tsx:160`), inside the 992–1199 band nobody designed. The move is cheap precisely because antd
already has `xl` = 1200: a mechanical rename across 36 sites plus one `Descriptions` prop, not a custom
breakpoint. Option (ii) leaves `lg` doing double duty — "is it a desktop?" and "is it a tablet?" — answering
"desktop" at 1024 and "phone" at 834, which is the accident 03 exists to remove.

**What it unblocks:** 03 itself (`03-responsive-mobile.md:67-69`), 04's ~10 tablet scenes (`04:37-38`), 05's
tablet pilot frame (`05:54-57`), and 06/08's viewport-switch honesty (`spec.md:387, 408-414`).

**If nobody answers:** 03 implements the spec's default (1200). Visibly safe — the reviewer sees a drawer and a
2-across stat row at 1024 — but expensive if the intended answer was (ii): the rework is ~36 prop sites plus
every tablet screen decision, and 04 would have been promised ~10 tablet scenes that turn out to be wide phones.



---

## Q3 — Do the responsive rules reach `web/` in this pass?

**The question as the spec words it** (`spec.md:762-766`): "They land in `GUIDELINES.md`, which is the contract
`web/` is built against, but nothing here ports them — that happens in the design port (`design-port`). So: a
responsive ticket there, or fold each screen's mobile/tablet rules into its existing design-port ticket (where
they would be verified against the real components rather than the mock)?"

**Evidence from the code — the port is live, and it inherited the prototype's responsive accident verbatim**

| evidence | where |
| --- | --- |
| Every `design-port` ticket is `closed` — 01 theme, 02 primitives, 03 data-layer, 04 router-and-shell, 05 auth, 06 project, 07 compose, 08 variables, 09 overview, 10 project-settings | `.scratch/design-port/issues/*.md` (`**Status:** closed` ×10) |
| The port carries the prototype's **single** breakpoint unchanged — same prop, same `collapsedWidth={0}` | `web/src/app/AppShell.tsx:191-192` |
| The port carries the Compose clamp, with a **test pinning the string** | `web/src/app/theme.ts:37`; `web/src/app/theme.test.ts:15` |
| The port carries the same `Col` rungs: Overview `lg={6} sm={12} xs={24}`, Compose `lg={15}`/`lg={9}`, Monitoring `lg={12} xs={24}`, Projects `lg={8} md={12}`, ServiceMonitor `lg={12}`, `Descriptions {lg:3,sm:2,xs:1}` | `web/src/pages/OverviewPage.tsx:113-122`; `web/src/pages/ComposePage.tsx:371, 443`; `web/src/pages/MonitoringPage.tsx:103, 108`; `web/src/pages/ProjectsPage.tsx:198`; `web/src/components/ServiceMonitor.tsx:218, 223`; `web/src/components/FullscreenEditor.tsx:209` |
| The port carries `body { min-width: 320px }` | `web/src/index.css:14` |
| And it *deliberately* inherited nothing else: "no new responsive rules" | `.scratch/design-port/issues/05-auth-screens.md:47` |
| The port's verification is per ticket and per *route*: typecheck, build, a DOM probe and a screenshot pass (the prototype's `scripts/screenshot.sh` technique) | `.scratch/design-port/spec.md:56-61`; `web-design/scripts/screenshot.sh:20-24` (`WIDTH`/`HEIGHT`, default 1600×1000) |
| The port has pure view modules with vitest seams, i.e. a width-driven rule *can* be unit-tested there — unlike the mock, where it can only be captured | `web/src/pages/*-view.ts`, `web/src/pages/*-view.test.ts` |

**What porting a responsive rule costs there, concretely**

- **It is not one rule, it is a sweep.** The rule is per screen per width (file 1 §8: 11 screens × 3 tiers) while
  the port's verification is a screenshot + DOM probe per ticket. Folding it into 06–09 means four re-opened
  tickets each re-running a sweep; one ticket runs it once.
- **Six tickets would have to be re-opened** — 05 (auth's 44 % panel, file 1 §8), 06 project, 07 compose,
  08 variables, 09 overview, 10 project-settings — each with a closed review gate and its own Comments.
- **Three couplings are cheaper done once:** the `lg`→`xl` rename touches the same six files in `web/` as in the
  mock; `theme.test.ts:15` pins the clamp string; and the clamp's replacement is used by `ComposePage.tsx`.
- **No Go cost**: `design-port/spec.md:19-20` is routing only, so no `go test` is dragged in.

**Recommendation: a new design-port ticket (11-responsive), created now and pointing at 03's `GUIDELINES.md` §6
text — not folded into the ten closed tickets.** Reason: the port's own gate is "per ticket, verified", so
re-opening 06–09 for a cross-cutting dimension repeats the same screen×width sweep four times and blurs four
closed reviews; one ticket keeps the sweep single, lets the `lg`→`xl` rename land once, and turns the port's
inherited `collapsedWidth={0}` (`web/src/app/AppShell.tsx:191-192`) into a tracked item rather than a surprise.
Folding in would only win if the work were genuinely screen-local, and file 1 shows it is not: the shell, the
breakpoint rung and the two clamps are shared by every screen.

**What it unblocks:** 09 (`design-board`'s ship-and-retire) can say the contract is *carried*, not merely agreed,
and `design-port` gets a home for §6's rules.

**If nobody answers:** the contract stops at `GUIDELINES.md`, and the shipped panel keeps
`breakpoint="lg" collapsedWidth={0}` (`web/src/app/AppShell.tsx:191-192`) — i.e. **production ships the exact bug
03 exists to fix**, at whatever width a user's window happens to be. This is the only one of the four questions
whose unanswered default is a user-visible defect rather than a review-surface cost.

---

## Q4 — What is the board's default mode?

**The question, as the spec words it.** The Ask in review was "can the frame be resized: mobile, tablet,
desktop, and dark, light?" (`spec.md:381-383`), and the spec's answer is "the board has two modes, and each
switch means the same thing in both, but only one of the modes can show anything" (`spec.md:385-386`). Three
candidate defaults:

- **(ก)** stills with a per-frame **Live** toggle — Still is "the default: what ships, and what works offline"
  (`spec.md:387`);
- **(ข)** live-by-default, with a mount cap;
- **(ค)** live for the click-heavy groups only.

**Evidence — the measured numbers, which are the whole argument** (`spec.md:456-462`)

| 18 frames of the same screens | measured | per frame (derived) |
| --- | --- | --- |
| live iframes (what the gallery does today) | **287 MB heap · 19 renderer processes · ~19 s to ready · +~0.5 GB RSS** | ~9 MB heap · 1 renderer · ~28 MB RSS |
| stills (WebP q90, 1.6 MB of files) | **0.5 MB heap · 1 process · 261 ms** | ~0.03 MB · shared process |

Extrapolations the spec already states: ~130 scenes live = **~1.2 GB heap and ~130 renderer processes**
(`spec.md:461-462`); the cap of **8** = **~73 MB heap** (`spec.md:409-414`, "a review tool … ~130 is a stress
test"). The cost drivers in the code and the plan:

| evidence | where |
| --- | --- |
| Live is **bounded by design**: "one frame in the lightbox by default, plus an optional *Live this group* / *Live the filtered set* capped at 8 with the count shown on the button" | `spec.md:409-412` |
| The board ships as **two artifacts**, the offline one a built folder — "no Node, no Chrome — opens `dist-single/index.html#/design/board` from disk"; offline there is **no dev server**, so "Live is unavailable and says so instead of showing a blank iframe" | `spec.md:556-560`, `:415-417` |
| Live is never the whole canvas — a non-goal | `spec.md:766-768` |
| Theme is cheap per *mounted* frame: `main.tsx` already listens for `relist:theme`, so dark appears "instantly, without a reload" — the cost is the mount, not the message | `spec.md:424-438`, `:390` |
| The still set is declared per scene by the registry, and a variant never captured is a labelled hole with a Live button — which only works if Still is the default | `04:13, 29-40, 68-71`; `spec.md:387` |
| "Click-heavy" is not a property the scene model has: `kind: 'screen' \| 'overlay' \| 'feedback'`, `viewports`, `ui`, `after`, `menuPath`, `caption`, `hash`, `title`, `group` — and nothing else | `04:7-22` |

**Recommendation: (ก) — stills by default, with the per-frame Live toggle plus the capped group-live (≤ 8).**

Reason: **the deliverable includes an offline folder with no dev server** (`spec.md:556-560`), so a live default
is broken exactly where the review is meant to be portable — which makes the default a question of "what works
everywhere", not "what is quicker to wire". The measured asymmetry points the same way: 19 s to ready for 18
frames against 261 ms for the same 18 as stills, and ~130 live frames would be ~1.2 GB of heap, which is not a
review surface. (ข) is (ก) with a different first paint, and its cap has to be low enough (≤ 8, `spec.md:411`)
that it stops being a default at all. (ค) cannot be specified from the data: "click-heavy" is not a registry
field (`04:7-22`), so it would become a hand-maintained list of scene ids — a second source of truth for a set
the registry already owns.

**What it unblocks:** 06 (the canvas) and 08 (board chrome). The default decides whether the canvas can render
anything before a still exists, whether `dist-single` is a working review surface, and whether 05 must finish
before 06 can be demonstrated; it also fixes what 05's manifest must carry (`05:38-43` — one entry per still,
`theme` always `light`).

**If nobody answers:** 06 implements stills-with-Live, the spec's stated default, and (ข)/(ค) become a later
change to the board's chrome rather than to the pipeline — so the cost is a re-open of 06. The risk is the
opposite default being chosen quietly in code: a live-by-default board that renders nothing when opened from
disk, which is the one failure the offline artifact cannot explain away.



---

## Summary

| # | question | recommendation | what it blocks | if nobody answers |
| --- | --- | --- | --- | --- |
| 1 | How wide is the still matrix? | **(a) as planned**; widen to (b) only if 05's pilot confirms non-desktop frames at ~⅓ of desktop bytes; **(c) rejected on value** — ~120 near-duplicate sheet frames | 04's `viewports` lists, 05's full run, 06/08's viewport switch, 09's committed size | 04 defaults to (a) and the board shows labelled holes + a Live button — honest, but the budget gate may be renegotiated on 05's numbers |
| 2 | Tablet's floor: 1200, or keep 992? | **floor 1200** (`lg`→`xl`, antd's own rung): 1024 today is the desktop grid with the navigation deleted | 03's own first acceptance item, 04's ~10 tablet scenes, 05's tablet frame, 06/08's switch honesty | 03 ships the spec's default (1200); if the answer was "keep 992", the rework is ~36 prop sites plus every tablet screen decision |
| 3 | Do the rules reach `web/` now? | **a new `design-port` ticket, 11-responsive**, pointing at §6 — not re-opening the ten closed ones | whether 09 can claim the contract is carried; where §6's rules live for the port | the contract dies in `GUIDELINES.md` and `web/` keeps `collapsedWidth={0}` — **production ships the bug 03 fixes** |
| 4 | The board's default mode | **(ก) stills + per-frame Live + group-live ≤ 8**: the offline folder has no dev server, and 18 live frames already cost 19 s and 287 MB | 06 (canvas) and 08 (chrome); whether `dist-single` is a real review surface | 06 ships stills-with-Live; the risk is a live default that renders nothing when opened from disk |

**Which two need the human first.** Q3 is the only one whose silence ships a defect to users; Q2 is the only one
whose wrong answer changes what ticket 03 *designs* rather than what it captures. Q1 and Q4 are answered well
enough by the spec's own defaults and by 05's five measured frames — take them as confirmations unless the
reviewer disagrees on principle.
