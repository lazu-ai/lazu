# Product component contracts

**Status: LOCKED**

**Locked: 2026-07-12**
**Production implementation:** `apps/web/src/components/ui/`

This file defines the behavior and appearance required of Lazu's shared product components. Page code composes the production implementation; it must not fork a private button, field, status, table, or overlay language.

## Core contracts

| Primitive               | Visual contract                                                                                                         | Interaction and accessibility contract                                                                                             |
| ----------------------- | ----------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| Button                  | 7px control radius; one ink/paper primary per task region; base-surface secondary with hairline; ghost only in dense tools | `focus-visible` evidence-color ring; disabled and loading are distinct; loading exposes `aria-busy`                                        |
| Field                   | 7px control; base surface; muted placeholder; error uses danger border plus an adjacent explanation                     | Every control has a programmatic label; invalid controls expose `aria-invalid`; errors are not color-only                          |
| Badge / status          | Badge radius 5px; pills only for filters and short state labels                                                         | Healthy circle, degraded diamond, down cross; visible text accompanies operational status                                          |
| Table                   | Yunmu row padding and text tokens; subtle header; hairline rows; selected-surface hover                                            | Header semantics remain native; row actions remain keyboard reachable; narrow layouts use readable cards or horizontal scroll      |
| Tabs                    | Ink underline for page peers; neutral segmented fill for 2–4 modes                                                   | Arrow-key behavior comes from the shared primitive; active state is not color-only                                                 |
| Overlay                 | 11px radius; semantic scrim; shadow only for real elevation                                                             | Popover for quick choice; centered editing/confirmation dialogs; inline read-only receipts; account-ledger drawer is the explicit Yunmu exception; focus is trapped and restored |
| Empty / error / loading | Follow paper-states.md; no decorative framing for frequent states; title, cause and recovery action when needed                                            | Real progress when measurable; skeleton for list loading; reduced-motion keeps essential feedback only                             |

## State matrix

Every interactive primitive must cover default, hover, focus-visible, active/selected, disabled and error where applicable. Components with asynchronous work also cover loading. Both light and dark mappings are generated from `brand/tokens/`; component code must not redefine theme colors.

## Verification surfaces

- `brand/foundations/component-contracts.card.html` is the compact Brand specimen.
- `apps/web/src/components/ui/lazu-ui.stories.tsx` is the runnable production specimen.
- `apps/web/src/components/ui/lazu-ui.contract.test.tsx` protects semantic state behavior.
- `apps/web/scripts/check-brand-contract.mjs` prevents legacy token/font dependencies from returning.

Model operations follows [model-ops-ledger](model-ops-ledger.md): a ledger whose rows open a centred dialog.
