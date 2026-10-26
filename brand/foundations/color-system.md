# Color system — G1

**Status: LOCKED** — follows the 2026-10-06 G1 decision in [BRAND.md](../BRAND.md),
which replaced the 2026-08-20 Yunmu ground, surfaces and evidence colour.

This is the shared palette for Console, public web and Docs. The former
warm-orange action palette is superseded. Do not copy its old hex table into
product code or introduce a separate palette for a route.

## Semantic rules

1. One flat neutral grey (`#F5F5F6`) forms the light ground, with white
   sheets on it; dark mode uses a near-black neutral (`#0F0F11`) with sheets
   one step up (`#18181B`). No gradients, lights or grid in either theme.
2. Primary buttons are ink, in every scope. Evidence, focus and selected
   controls use teal (`--lazu-evidence`); teal is not a button fill and never a
   large area. Vermilion `#C9452A` is not a token: it appears only on the home
   page hero eyebrow and the headline's emphasis.
3. Selection uses background, weight and foreground — never rails or arrows.
   The current navigation item is a teal wash with teal type.
4. Charts use teal (`--lazu-chart-1`) for the primary measure and neutral
   auxiliary series. Legacy orange data tokens do not authorize orange chart
   defaults.
5. Success, warning and danger communicate status, accompanied by text or shape.
6. Surfaces, borders and overlays use semantic tokens. Shadows express real
   elevation; pages do not invent private color values.
7. Feedback follows [feedback-system.md](feedback-system.md); empty, error and
   loading surfaces follow [paper-states.md](paper-states.md).

## Value authority

Exact light/dark values live in [colors.css](../tokens/colors.css). Chart and
component tokens also live in
[data-viz.css](../tokens/data-viz.css) and [ui.css](../tokens/ui.css).
Use these sources instead of maintaining a second hex table in prose.

Composition and exceptions are defined by [Yunmu](yunmu-console.md) and the
applicable later decisions in [BRAND.md](../BRAND.md).
