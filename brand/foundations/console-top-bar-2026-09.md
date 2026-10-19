# Console top bar and members empty state — September 2026

Status: LOCKED by user approval on 2026-09-20.

Source: two rounds of options drawn against the running console. Round one put
four page-head treatments and four members empty states side by side; round two
widened the question to the whole top of the console (four shell directions,
then four elaborations of the chosen one) and detailed its three parts. The
user selected one option per question. Those canvases are decision history;
this document is the production authority.

## Selected directions

| Question | Selected |
| --- | --- |
| Top of the console | **T4** — a bar in the main column, then the page's own head, then its tabs |
| Where the project is named | **X1** — the bar's first crumb is the project switcher |
| Where the build number lives | **V** — the help menu's last line |
| Search in the bar | **dropped** — not built |
| 成员与预算 with no other member | **E2** — the members table, holding the one row that exists |

## Scope amendment

`console-review-2026-09.md` says "Preserve sidebar version/announcement entry,
theme and language controls." That clause is superseded here, and only that
clause: everything else in that document stands, including the logout
confirmation popover, the dialog rules and the Keys decisions.

The controls are not removed. They move to the console top bar, which did not
exist when the September 6th review was written, and which now carries
announcements, theme, language, help and the account. The reason for the move
is that the sidebar had become the place things went when they belonged to no
page: a version plate wedged into the brand row carried the unread state, and
theme and language sat under the navigation in a utility strip. The bar gives
them a row of their own at the top of the column they act on.

**The mobile drawer keeps the sidebar arrangement.** The bar is desktop-only,
so on a phone the drawer still holds the project switcher and the logout
control, and the mobile top chrome still holds the version plate, theme and
language. Nothing that exists only in the bar may be the sole route to an
action.

## The bar

52px, inside the main column, not across the window. No background and no
bottom rule: the page head below owns the one horizontal rule.

Left, as one line: the project switcher (20px tile, name, chevron), `›`, the
name of the sidebar group the current route belongs to.

Right, 32px icon buttons, 10px radius, no border: announcements, theme,
language, help, a 1px divider, then a 28px round avatar. Announcements carry a
6px evidence dot when unread, expanding once on appearance rather than
blinking.

Every menu item that navigates warms its route on hover and focus, and each is
hidden when its destination is hidden: the wallet by scope and by module, model
status by module.

## The page head

10px above a 28px/620 title. Beside the title, one to three of the page's own
numbers as `console-stat` chips — a label at 11px and a mono value. The page's
primary action sits at the right of the title row.

Every head sits on a rule. A page with peer views draws it as the tab row
underneath; a page without them draws it on the head itself. An inner page
mounted as a tab body draws none, because the outer page already did.

## 成员与预算 before there is anyone else

The page is the members table it will become, composed from the same `Card` and
`.pj-table` the shared members page uses, with three rows: the owner, an invite
row (address, role, cap, invite) where the next member will appear, and one
greyed row showing the shape of that seat. One sentence under the table carries
the money. The project's name is renamed on the head; it is not a field on the
way to inviting someone.

The cap offers `$50 / $100 / $500` plus, when the project's configured default
is something else, that value — a custom amount or 不限 — so nothing is sent
that the reader cannot see. The row is disabled until the project's settings
have actually arrived; a guessed cap is never offered as the choice.

Every later invitation is a 440px single-column dialog: address, role, cap, a
full-width send button, one line of consequence. No eyebrow, no second column,
no cancel button beside send.
