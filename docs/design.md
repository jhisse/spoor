# spoor design system

The stylesheet, `internal/web/static/spoor.css`, is the source of truth: where this page and the application differ, the application is right.

`docs/design/contrast.py` holds the colour token table and checks its contrast; `--css` prints the block at the top of `spoor.css`.

## 1. Direction

**Quiet instrument.** Neutral surfaces, hairlines instead of boxes, system sans with tabular numerals, one water-blue accent that is used only for interaction. Chrome recedes so that the data is the only colourful thing on the page; prompts are set as reading text, not as log lines. Light and dark are the same design: every token has both values and every pair is contrast-checked.

Three things make spoor recognisable:

1. **The token bar.** Four segments in one water hue, stronger = dearer per token, cache read hatched. It is the only hatched thing and the only teal data in the UI, and it appears at four sizes with the same order everywhere.
2. **The ledger tree.** Single-line rows whose duration, position, token bar and cost sit in fixed columns that line up at every depth, with a column header.
3. **Kind as a lettered tag, status as a shape.** A small outlined letter (G, T, R, E, K, A, C, S) in the kind's hue; a cross, a ring, a disc and a diamond for status. Neither depends on colour.

## 2. Tokens

Source of truth: the table in `docs/design/contrast.py`; `--css` emits the block at the top of `spoor.css`. Do not edit the values in the CSS.

| Group | Tokens | Rule of use |
|---|---|---|
| Surfaces | `surface-0` page, `surface-1` panel, `surface-2` sunken / hover / table header, `surface-3` active, bar track | Dark: higher is lighter. No shadows on the page (only the service menu floats). No opacity on fills. |
| Lines | `line` separators, `line-strong` button borders and rails, `line-control` input borders | Only `line-control` is ≥ 3:1; it is used where the border is the control's only boundary. |
| Ink | `ink-1` content, `ink-2` labels and secondary, `ink-3` captions | All three are ≥ 4.5:1 on all four surfaces. There is no fainter text. |
| Accent | `accent`, `accent-strong`, `accent-ink`, `accent-soft` | Links, the one primary button, selection, focus ring. Never data, never a message role. |
| Status | `ok`, `err`, `warn`, `info` + `-soft`, `neutral` | Red = error only. Amber = "worth a look", "estimated", orphan. Green = ok / live / active. `info` = the "new" marker. `neutral` = unset, duration bars. |
| Span kinds | `kind-{generation,tool,retriever,embedding,reranker,agent,chain,generic}` + `-soft` | **Fills, outlines and marks only (≥ 3:1). Text beside a kind mark is always ink.** |
| Token buckets | `tok-cache-read` (+`-soft`, hatched), `tok-fresh`, `tok-cache-write`, `tok-output` | Only in the token bar and the context charts. Fixed order. |
| Chat roles | no tokens of their own | A 2 px rule: system `line-strong`, user `ink-1`, assistant `kind-generation`, tool `kind-tool`. |
| Heat ramp | `heat-1..6`, `heat-ink` | Only in the heat map. `ink-1` on steps 1–3, `heat-ink` on 5–6. |

Kind colour is never text, so it needs 3:1, not 4.5:1. `kind-generic` and `tok-cache-read` are both greys: they are told apart by mark (a lettered tag against a hatched segment), not by colour. The check is in §7.

## 3. Type, space, shape

| | Value |
|---|---|
| Fonts | `system-ui` stack for everything; `ui-monospace` stack only for code, JSON, ids, model names, key/value trees. No web font. |
| Sizes (px / line) | 20/28 page title · 16/24 section title · 14/22 prompt and completion text · 13/20 interface and tables · 12/16 labels and captions · 11 only inside SVG charts and `kbd` |
| Weights | 400, 600 (500 on tabs and buttons). |
| Numbers | `font-variant-numeric: tabular-nums` on `body`; right-aligned in tables; sans, same size as the row. |
| Labels | Sentence case, 12 px semibold `ink-2` (`h3`). No uppercase tracked micro-text anywhere. |
| Measure | Prose `max-width: 80ch`; explanatory paragraphs 72ch. Code is exempt. |
| Spacing | 2, 4, 8, 12, 16, 24, 32, 48 px. Inside a component 4–8; between blocks 12–16; page bottom 48. Panel padding 16; table cell 12 horizontal; page gutter 16, 24 from 1280 px. |
| Radius | 6 px panels and controls; 4 px chips, tags, tree rows; 1–2 px chart marks. |
| Borders | 1 px. At most one border between content and page: no panel inside a panel. Inside a panel use a hairline (`.sec`, `.band`) or a sunken block (`.code`, `.call`). |
| Focus | `:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px }` on every focusable element, defined once. |
| Targets | Buttons 32 px (forms) and 28 px (toolbars, rows); inputs 32; table rows 36; tree rows 28; segmented items 24×28. Histogram bars and heat-map cells are narrower than 24 px and rely on the WCAG 2.5.8 "equivalent control" exception: "As a list" under the charts has the same links as full-width lines of 32 px or more, and the date and range filters reach the same targets. |
| Motion | 100 ms on colour, background and border of interactive elements; nothing on swapped content; all off under `prefers-reduced-motion`. |

## 4. Layout

**App shell.** Sticky opaque header, 48 px: mark + "spoor", the **service menu** (a `<details>`; lists the services traces were sent by and "All services"; a long name is cut with an ellipsis, in the header at 22 characters, and carries its full text in a title), the sections as text tabs (**Traces · Sessions · Blind spots · Help**, current one `ink-1` with a 2 px accent underline), and at the far right the **theme control** (three icon buttons: system, light, dark). Under 768 px the tabs drop to a second 40 px row. With "All services" selected the list gains a Service column; the tabs keep the selected service. A skip link is the first focusable element. Breadcrumbs (12 px, `›`) sit above the title only where there is a parent: `Traces › Session … › trace`, `Sessions › session`.

**Page widths.** List, sessions, session: 1280 px. Blind spots: 920 px. Trace: 1680 px.

**Trace workspace.** Top to bottom: breadcrumb; title row (status glyph, name, short id, Previous / Next with printed `←` `→`); **what the person asked** (`.asked`, 14 px, two lines at most, whole in the title; absent when nothing recorded it or it is the trace's name); a panel with the stats and the metadata disclosure; one line (`.bar`) with the **Tree | Read switch** and the "Worth a look" pointers, which wrap under the switch when they do not fit beside it; with a search, the search band; then the workspace. The **context per step** is open by default (`.ctxband`, a `<details open>`): it is what spoor shows that a bill cannot, so it sits in the first screen, above the selected span, and its bars select spans.

- The workspace is a grid `clamp(420px, 40%, 620px) | 1fr`. **From 1024 px the tree view fits the window** (`#trace-detail.fit` is as tall as the viewport under the header and the workspace takes what the lines above leave, at least 280 px), so the page itself does not scroll: an arrow key moves the selection and nothing else. **Each pane is its own scroll container**, so the tree and the selected span are always side by side; the right column is the context chart (at most 46% of the viewport, scrolling inside) above the selected span's pane, and each has a sticky pane header (tree: fold counts, column header). The selected span's pane starts with its path (`.crumbs`, ancestors as links) and its place, "Span 4 of 65", in the tree's display order.
- Charts: the context chart is open in the right column; "The same steps in dollars" is a closed disclosure under it. The Read view, which scrolls as a document, starts with the same chart. Trace metadata is a disclosure in the stats band. On the session page, which is a scrolling document, the same chart is open by default and its reading notes are a closed disclosure under it.
- **1024–1279 px:** the tree drops its Position column. **With a search** (span filters in the URL) the search band sits under the view switch, and the selected span's pane marks the words (`mark`) and opens any disclosure that holds one. **Under 1024 px:** one column, **the context chart and the selected span first, the tree after them**, all at natural height, and the page scrolls; the span header gets a "Span tree ↓" link. **Under 560 px:** the tree keeps name, time and cost only, and key/value pairs stack.
- The Read view replaces the two panes with one column: the model calls as a transcript, under the same view switch. **Under 560 px** the stats tighten (a shorter token bar, so Spans joins the line of Tokens), so the question, the figures and the switch fit the first screen.

## 5. Chart grammar and accessibility

- **Hue = span kind. Shape = status. Length on a shared scale = magnitude.** Red is never a category.
- Never colour alone: kind has its letter and the legend; error has a cross, a cap or a reason; cache read has the hatch and its fixed position; C / B / E are letters.
- In the tree only error is marked. "Unset" is the normal OTLP status, and a ring on every row would be noise. Lists and turn strips show all three shapes.
- Every chart SVG has `role="img"` and an `aria-label`; every segment or bar has a `<title>`; clickable marks are real links.
- Chart text is 11 px `ink-2`; every chart states its scale (axis ticks, or "bar 0–N" in the column header).
- No track behind a magnitude bar. One exception, part-of-whole: the position rail in the tree.
- Legends are HTML, under or beside the chart, built from `.sw` swatches, and never contain an inline `<svg>`.
- Honesty labels are kept as text: "estimated", "No basis for comparison", "? = not reported by the SDK", "– = no price for the model", "Showing 21 of 31 spans…", "pointers, not verdicts", "Show 318 more characters".

- Formats, one per quantity: a duration is a number, a space and a unit, at most two units (`204 ms`, `1.2 s`, `2 min 31 s`, `1 h 05 min`); tree columns use one unit (`2.5 min`) and fewer decimals for a cost of a dollar or more (`Σ $12.54`), with the full figure in the title; tokens read `1.17M in · ? out`.
- Keyboard: chart bars, histogram bars and heat-map cells are links outside the tab order: "As a list" under the charts has every one as a line of its own (the periods that have traces, and every cell), and the date and range fields of "More filters" reach the same targets. On the trace page `←` `→` change trace, `↑` `↓` change span, and with a search `n` `p` change match. A shortcut does nothing with Ctrl, Alt or Cmd held, or while a field, button or disclosure has focus (`kbd_ok` in `detail.html`).

## 6. Theme and CSS delivery

**Theme.** `color-scheme: light dark` on `:root` and `light-dark()` in every token: each token is declared once and the page follows the OS. `<html data-theme="light|dark">` forces one. The server sets that attribute from the `spoor_theme` cookie written by `POST /theme` (three buttons in the header; `system` deletes the cookie; `303` back to the page). No script, no flash. Requires a browser with `light-dark()`: Chrome or Edge 123, Firefox 120, Safari 17.5.

**CSS.** One hand-written stylesheet, no build step, embedded in the binary and served at `/static/spoor.css`.

- Every class a template or a Go renderer writes is defined in `spoor.css`, and a `style` attribute may only set custom properties. `internal/web/style_test.go` fails otherwise.
- Colour is a class; geometry is an SVG attribute computed in Go (`width`, `x`, `height`). Classes for data colour: `.k k-<kind>`, `.tk-fresh`, `.tk-write`, `.tk-out`, `.f-ok`, `.f-err`, `.f-dur`. The cache-read hatch is `fill="url(#hatch)"`.
- Status and theme glyphs come from one sprite (`{{define "icons"}}` in `layout.html`: seven symbols and the hatch pattern), used with `<use href="#i-err">`. `Σ`, `!`, `×n`, arrows and `C`/`B`/`E` are text.
- The static export (`spoor export --html`) is one file with the stylesheet and the sprite inlined; spans are panels indented by `--depth`.

## 7. Contrast

`python3 docs/design/contrast.py` computes WCAG 2.x contrast from the rounded hex values and exits 1 on any failure; `--md` prints every pair, the token values and the colour-vision tables.

Not enforced, by design: `line` and `line-strong` against surfaces (1.2–1.7:1, decorative), steps between surfaces, heat steps 1–3 against the panel, and a kind mark on `surface-3` (kind marks never sit on it).

Minimum distance between the eight kinds under simulated colour-vision deficiency: 5.1 (light, tritan) and 7.9 (dark, deutan) on the OKLab×100 scale.
