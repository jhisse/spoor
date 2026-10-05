#!/usr/bin/env python3
"""Source of truth and verifier for spoor's proposed colour tokens.

Tokens are authored in OKLCH (L, C, H). The script
  1. maps each one into the sRGB gamut (chroma reduction, then 8-bit rounding),
  2. checks WCAG 2.x contrast for every declared pair, FROM THE ROUNDED HEX,
  3. simulates protan/deutan/tritan vision (Machado, Oliveira & Fernandes 2009,
     severity 1.0) and reports the minimum pairwise OKLab distance of the
     categorical palettes,
  4. emits the CSS (--css) or the Markdown tables for the notes (--md).

Stdlib only.  python3 contrast.py [--css | --md]
Exit status 1 if any enforced pair is under its threshold.
"""
import math
import sys
from itertools import combinations

# ----------------------------------------------------------------- colour math
# Oklab matrices: Björn Ottosson, https://bottosson.github.io/posts/oklab/


def _lin(c):  # sRGB transfer function, WCAG 2.x / IEC 61966-2-1
    return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4


def _gam(c):
    return 12.92 * c if c <= 0.0031308 else 1.055 * c ** (1 / 2.4) - 0.055


def oklab_to_linear(L, a, b):
    l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
    m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
    s = (L - 0.0894841775 * a - 1.2914855480 * b) ** 3
    return (4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
            -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
            -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s)


def linear_to_oklab(r, g, b):
    l = math.copysign(abs(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b) ** (1 / 3), 1)
    m = math.copysign(abs(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b) ** (1 / 3), 1)
    s = math.copysign(abs(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b) ** (1 / 3), 1)
    return (0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s,
            1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s,
            0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s)


def oklch_to_hex(L, C, H):
    """Reduce chroma until the colour fits sRGB, then round to 8 bits."""
    def lin(c):
        return oklab_to_linear(L, c * math.cos(math.radians(H)), c * math.sin(math.radians(H)))

    def fits(c):
        return all(-1e-4 <= v <= 1 + 1e-4 for v in lin(c))
    if not fits(C):
        lo, hi = 0.0, C
        for _ in range(30):
            mid = (lo + hi) / 2
            lo, hi = (mid, hi) if fits(mid) else (lo, mid)
        C = lo
    return "#%02x%02x%02x" % tuple(round(255 * _gam(min(1, max(0, v)))) for v in lin(C))


def hex_to_linear(h):
    return tuple(_lin(int(h[i:i + 2], 16) / 255) for i in (1, 3, 5))


def hex_to_oklch(h):
    L, a, b = linear_to_oklab(*hex_to_linear(h))
    C = math.hypot(a, b)
    return L, C, (math.degrees(math.atan2(b, a)) % 360 if C > 5e-4 else 0.0)


def css_oklch(h):
    L, C, H = hex_to_oklch(h)
    return "oklch(%.3f %.3f %.0f)" % (L, C, H)


def luminance(h):  # WCAG 2.x relative luminance
    r, g, b = hex_to_linear(h)
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(a, b):
    la, lb = sorted((luminance(a), luminance(b)), reverse=True)
    return (la + 0.05) / (lb + 0.05)


# Machado, Oliveira & Fernandes (2009), severity 1.0, as published at
# https://www.inf.ufrgs.br/~oliveira/pubs_files/CVD_Simulation/CVD_Simulation.html
# Applied to LINEAR sRGB (the convention of colorspacious and DaltonLens; the
# page itself does not state the encoding).
CVD = {
    "protan": ((0.152286, 1.052583, -0.204868), (0.114503, 0.786281, 0.099216), (-0.003882, -0.048116, 1.051998)),
    "deutan": ((0.367322, 0.860646, -0.227968), (0.280085, 0.672501, 0.047413), (-0.011820, 0.042940, 0.968881)),
    "tritan": ((1.255528, -0.076749, -0.178779), (-0.078411, 0.930809, 0.147602), (0.004733, 0.691367, 0.303900)),
}


def seen_as(h, kind):
    rgb = hex_to_linear(h)
    if kind != "normal":
        rgb = tuple(min(1, max(0, sum(m * c for m, c in zip(row, rgb)))) for row in CVD[kind])
    return linear_to_oklab(*rgb)


def delta(a, b, kind):  # Euclidean distance in OKLab x100 (delta-E OK)
    return 100 * math.dist(seen_as(a, kind), seen_as(b, kind))


# --------------------------------------------------------------------- tokens
# name: ((L, C, H) light, (L, C, H) dark)
N = 260  # neutral hue: a faint cool cast, as in the current zinc-like palette
T = {
    # surfaces, increasing elevation. Dark: higher = lighter (no shadows).
    "surface-0": ((0.985, 0.002, N), (0.165, 0.004, N)),
    "surface-1": ((1.000, 0.000, N), (0.205, 0.005, N)),
    "surface-2": ((0.965, 0.003, N), (0.240, 0.006, N)),
    "surface-3": ((0.925, 0.005, N), (0.290, 0.007, N)),
    "line": ((0.915, 0.005, N), (0.285, 0.006, N)),
    "line-strong": ((0.860, 0.007, N), (0.360, 0.008, N)),
    "line-control": ((0.600, 0.012, N), (0.560, 0.012, N)),
    "ink-1": ((0.220, 0.012, N), (0.950, 0.004, N)),
    "ink-2": ((0.400, 0.014, N), (0.800, 0.010, N)),
    "ink-3": ((0.500, 0.014, N), (0.700, 0.012, N)),
    "neutral": ((0.580, 0.014, N), (0.640, 0.012, N)),
    # one accent
    "accent": ((0.500, 0.100, 222), (0.790, 0.110, 215)),
    "accent-strong": ((0.420, 0.090, 222), (0.870, 0.090, 212)),
    "accent-ink": ((1.000, 0.000, N), (0.200, 0.040, 222)),
    "accent-soft": ((0.960, 0.025, 215), (0.280, 0.040, 222)),
    # semantic
    "ok": ((0.500, 0.130, 155), (0.810, 0.150, 158)),
    "ok-soft": ((0.960, 0.035, 155), (0.275, 0.045, 158)),
    "err": ((0.490, 0.190, 27), (0.700, 0.170, 25)),
    "err-soft": ((0.960, 0.022, 20), (0.285, 0.055, 22)),
    "warn": ((0.535, 0.115, 78), (0.850, 0.130, 85)),
    "warn-soft": ((0.965, 0.040, 88), (0.290, 0.045, 75)),
    "info": ((0.500, 0.150, 255), (0.770, 0.110, 255)),
    "info-soft": ((0.960, 0.022, 255), (0.285, 0.045, 255)),
    # span kinds: a FILL/MARK colour (>= 3:1), never a text colour. Text next
    # to a kind mark is neutral ink, which is what lets the light values be
    # bright instead of the dark browns a 4.5:1 text colour forces.
    "kind-generation": ((0.560, 0.210, 285), (0.676, 0.178, 278)),
    "kind-tool": ((0.640, 0.165, 50), (0.682, 0.160, 44)),
    "kind-retriever": ((0.615, 0.135, 165), (0.895, 0.136, 157)),
    "kind-embedding": ((0.63, 0.19, 340), (0.725, 0.158, 337)),
    "kind-reranker": ((0.500, 0.105, 105), (0.910, 0.153, 96)),
    "kind-agent": ((0.44, 0.12, 255), (0.761, 0.139, 244)),
    "kind-chain": ((0.411, 0.012, N), (0.796, 0.006, N)),
    "kind-generic": ((0.545, 0.012, 260), (0.658, 0.006, N)),
    # token buckets: one hue, ordered by price per token (see notes)
    "tok-cache-read": ((0.610, 0.025, 215), (0.600, 0.040, 215)),
    "tok-cache-read-soft": ((0.950, 0.020, 215), (0.270, 0.030, 215)),
    "tok-fresh": ((0.520, 0.110, 220), (0.700, 0.110, 216)),
    "tok-cache-write": ((0.385, 0.085, 230), (0.830, 0.095, 205)),
    "tok-output": ((0.240, 0.030, 240), (0.950, 0.020, 205)),
}
KINDS = ["generation", "tool", "retriever", "embedding", "reranker", "agent", "chain", "generic"]
BUCKETS = ["tok-cache-read", "tok-fresh", "tok-cache-write", "tok-output"]
for k in KINDS:  # badge tint behind each kind: same hue, fixed L and C
    h = (T["kind-" + k][0][2], T["kind-" + k][1][2])
    c = 0.004 if k in ("chain", "generic") else None
    T["kind-%s-soft" % k] = ((0.955, c or 0.028, h[0]), (0.285, c or 0.045, h[1]))

# sequential ramp for heat maps: one hue, equal OKLCH lightness steps
HEAT_H = 265
for i in range(6):
    T["heat-%d" % (i + 1)] = ((0.930 - 0.100 * i, 0.030 + 0.024 * i, HEAT_H),
                              (0.300 + 0.100 * i, 0.050 + 0.010 * i, HEAT_H))
T["heat-ink"] = ((1.000, 0.000, N), T["surface-0"][1])  # text on the strong steps

HEX = {k: {"light": oklch_to_hex(*v[0]), "dark": oklch_to_hex(*v[1])} for k, v in T.items()}

# ---------------------------------------------------------------------- pairs
PAIRS = []  # (foreground, background, minimum ratio, what it is)


def need(fg, bgs, ratio, what):
    PAIRS.extend((fg, bg, ratio, what) for bg in bgs)


SURF = ["surface-0", "surface-1", "surface-2"]
SOFTS = [k for k in T if k.endswith("-soft") and k != "tok-cache-read-soft"]
for ink in ("ink-1", "ink-2", "ink-3"):
    need(ink, SURF + ["surface-3"], 4.5, "text")
for ink in ("ink-1", "ink-2"):
    need(ink, SOFTS, 4.5, "text on tint")
for s in ("accent", "ok", "err", "warn", "info"):
    need(s, SURF + [s + "-soft"], 4.5, "coloured text / icon")
need("accent-strong", SURF + ["accent-soft"], 4.5, "link hover")
need("accent-ink", ["accent", "accent-strong"], 4.5, "text on solid accent")
MARK_BG = SURF + ["accent-soft"]  # accent-soft is the selected row
for k in KINDS:
    need("kind-" + k, MARK_BG + ["kind-%s-soft" % k], 3.0, "kind mark / bar")
for b in BUCKETS:
    need(b, MARK_BG, 3.0, "token bar segment")
need("tok-cache-read", ["tok-cache-read-soft"], 3.0, "hatch line on its ground")
for s_ in ("ok", "err", "warn", "neutral"):
    need(s_, ["accent-soft"], 3.0, "status glyph on selected row")
need("neutral", SURF, 3.0, "status 'unset' ring")
need("line-control", SURF, 3.0, "form control border")
need("ink-3", ["accent-soft"], 4.5, "text on selected row")
need("accent", SURF, 3.0, "focus ring")
REPORT_ONLY = [("line", "surface-1"), ("line-strong", "surface-1"), ("line", "surface-0"),
               ("surface-1", "surface-0"), ("surface-2", "surface-1"), ("surface-3", "surface-1")]


def heat_ink(i, theme):
    """The ink to print on heat step i: ink-1 while it reaches 4.5:1, else heat-ink."""
    step = HEX["heat-%d" % i][theme]
    return max(("ink-1", "heat-ink"), key=lambda k: contrast(HEX[k][theme], step))


def run_pairs():
    rows, failed = [], 0
    for fg, bg, minimum, what in PAIRS:
        r = [contrast(HEX[fg][t], HEX[bg][t]) for t in ("light", "dark")]
        ok = min(r) >= minimum
        failed += not ok
        rows.append((what, fg, bg, minimum, r[0], r[1], ok))
    for i in range(1, 7):
        r = [contrast(HEX[heat_ink(i, t)][t], HEX["heat-%d" % i][t]) for t in ("light", "dark")]
        failed += min(r) < 4.5
        rows.append(("number in heat cell", "%s / %s" % (heat_ink(i, "light"), heat_ink(i, "dark")),
                     "heat-%d" % i, 4.5, r[0], r[1], min(r) >= 4.5))
    return rows, failed


def cvd_table(names):
    out = []
    for theme in ("light", "dark"):
        for kind in ("normal", "protan", "deutan", "tritan"):
            d, a, b = min((delta(HEX[a][theme], HEX[b][theme], kind), a, b) for a, b in combinations(names, 2))
            out.append((theme, kind, d, a, b))
    return out


def cross_min(xs, ys):
    return [(theme, kind) + min((delta(HEX[a][theme], HEX[b][theme], kind), a, b) for a in xs for b in ys)
            for theme in ("light", "dark") for kind in ("normal", "protan", "deutan", "tritan")]


# --------------------------------------------------------------------- output
GROUPS = [
    ("Surfaces, increasing elevation (dark: higher is lighter)", ["surface-0", "surface-1", "surface-2", "surface-3"]),
    ("Lines: line and line-strong are decorative; line-control is >= 3:1", ["line", "line-strong", "line-control"]),
    ("Text: all three are >= 4.5:1 on every surface", ["ink-1", "ink-2", "ink-3", "neutral"]),
    ("Accent", ["accent", "accent-strong", "accent-ink", "accent-soft"]),
    ("Semantic", ["ok", "ok-soft", "err", "err-soft", "warn", "warn-soft", "info", "info-soft"]),
    ("Span kinds: fills and marks only (>= 3:1), always paired with a letter; text beside them is ink",
     ["kind-" + k for k in KINDS] + ["kind-%s-soft" % k for k in KINDS]),
    ("Token buckets (one hue, stronger = dearer per token; cache read is hatched)",
     BUCKETS[:1] + ["tok-cache-read-soft"] + BUCKETS[1:]),
    ("Heat-map ramp (single hue, equal lightness steps; 0 = surface-2)", ["heat-%d" % i for i in range(1, 7)] + ["heat-ink"]),
]
def emit_css():
    p = print
    p("/* Colour tokens. GENERATED by docs/design/contrast.py --css: edit the")
    p("   table there, run it (it fails if a pair drops under WCAG AA), paste here. */")
    p(":root {")
    p("  color-scheme: light dark;")
    for title, names in GROUPS:
        p("  /* %s */" % title)
        for n in names:
            p("  --%s: light-dark(%s, %s);" % (n, HEX[n]["light"], HEX[n]["dark"]))
    p("}")
    p(':root[data-theme="light"] { color-scheme: light; }')
    p(':root[data-theme="dark"] { color-scheme: dark; }')


def emit_md():
    p = print
    rows, failed = run_pairs()
    p("#### Token values\n")
    p("| Token | Light | Light OKLCH | Dark | Dark OKLCH |\n|---|---|---|---|---|")
    for _, names in GROUPS:
        for n in names:
            li, da = HEX[n]["light"], HEX[n]["dark"]
            p("| `--%s` | `%s` | %s | `%s` | %s |" % (n, li, css_oklch(li)[6:-1], da, css_oklch(da)[6:-1]))
    p("\n#### Enforced contrast pairs (%d pairs, %d failing)\n" % (len(rows), failed))
    p("| Use | Foreground | Background | Min | Light | Dark |\n|---|---|---|---|---|---|")
    for what, fg, bg, minimum, l, d, ok in rows:
        p("| %s | %s | %s | %.1f | %.2f | %.2f |%s" % (what, fg, bg, minimum, l, d, "" if ok else " **FAIL**"))
    p("\n#### Worst case per group\n")
    p("| Use | Min | Worst light | Worst dark |\n|---|---|---|---|")
    for what in dict.fromkeys(r[0] for r in rows):
        g = [r for r in rows if r[0] == what]
        wl, wd = min(g, key=lambda r: r[4]), min(g, key=lambda r: r[5])
        p("| %s | %.1f | %.2f (%s on %s) | %.2f (%s on %s) |" % (what, g[0][3], wl[4], wl[1], wl[2], wd[5], wd[1], wd[2]))
    p("\n#### Reported, not enforced (decorative separators and elevation steps)\n")
    p("| Pair | Light | Dark |\n|---|---|---|")
    for a, b in REPORT_ONLY:
        p("| %s vs %s | %.2f | %.2f |" % (a, b, contrast(HEX[a]["light"], HEX[b]["light"]), contrast(HEX[a]["dark"], HEX[b]["dark"])))
    p("\n#### Heat ramp\n")
    p("| Step | Light | L | vs surface-1 | Ink to use | Ratio | Dark | L | vs surface-1 | Ink to use | Ratio |\n|---|---|---|---|---|---|---|---|---|---|---|")
    for i in range(1, 7):
        cells = []
        for t in ("light", "dark"):
            h, ink = HEX["heat-%d" % i][t], heat_ink(i, t)
            cells.append("`%s` | %.3f | %.2f | %s | %.2f" % (h, hex_to_oklch(h)[0], contrast(h, HEX["surface-1"][t]), ink, contrast(HEX[ink][t], h)))
        p("| %d | %s | %s |" % (i, cells[0], cells[1]))
    kinds = ["kind-" + k for k in KINDS]
    for title, table in (("8 span kinds", cvd_table(kinds)), ("5 hued step kinds + agent", cvd_table(kinds[:6])),
                         ("4 token buckets", cvd_table(BUCKETS)), ("any kind vs any bucket", cross_min(kinds, BUCKETS)),
                         ("ok / err / warn / neutral (status)", cvd_table(["ok", "err", "warn", "neutral"]))):
        p("\n#### Minimum pairwise distance, %s (OKLab x100)\n" % title)
        p("| Theme | Vision | Min | Closest pair |\n|---|---|---|---|")
        for theme, kind, d, a, b in table:
            p("| %s | %s | %.1f | %s / %s |" % (theme, kind, d, a, b))


def self_check():
    assert abs(contrast("#000000", "#ffffff") - 21) < 1e-9
    assert abs(contrast("#777777", "#ffffff") - 4.478) < 0.001  # the well-known just-failing grey
    assert oklch_to_hex(1, 0, 0) == "#ffffff" and oklch_to_hex(0, 0, 0) == "#000000"
    L, C, H = hex_to_oklch("#ff0000")  # Ottosson / CSS Color 4: red = oklch(0.628 0.258 29.2)
    assert abs(L - 0.628) < 0.001 and abs(C - 0.2577) < 0.001 and abs(H - 29.23) < 0.05
    assert oklch_to_hex(*hex_to_oklch("#0e7490")) == "#0e7490"  # round trip
    # a neutral stays neutral for every simulated observer (matrix rows sum to 1)
    assert all(delta("#808080", "#808080", k) == 0 and math.dist(seen_as("#808080", k), seen_as("#808080", "normal")) < 1e-3 for k in CVD)


if __name__ == "__main__":
    self_check()
    if "--css" in sys.argv:
        emit_css()
    elif "--md" in sys.argv:
        emit_md()
    else:
        rows, failed = run_pairs()
        for what, fg, bg, minimum, l, d, ok in rows:
            if not ok:
                print("FAIL %-24s %-22s on %-22s need %.1f  light %.2f  dark %.2f" % (what, fg, bg, minimum, l, d))
        print("%d pairs, %d failing; worst light %.2f, worst dark %.2f (text pairs: %.2f / %.2f)" % (
            len(rows), failed, min(r[4] for r in rows), min(r[5] for r in rows),
            min(r[4] for r in rows if r[3] == 4.5), min(r[5] for r in rows if r[3] == 4.5)))
        for title, names in (("kinds", ["kind-" + k for k in KINDS]), ("buckets", BUCKETS)):
            for row in cvd_table(names):
                print("%-8s %-5s %-6s min %.1f  %s / %s" % ((title,) + row))
        for row in cross_min(["kind-" + k for k in KINDS], BUCKETS):
            print("kind-vs-bucket %-5s %-6s min %.1f  %s / %s" % row)
    sys.exit(1 if run_pairs()[1] else 0)
