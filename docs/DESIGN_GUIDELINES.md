# Staff App — Design Guidelines

> **Scope: staff / admin UI only.** These rules apply to the staff app
> ([`web/staff`](../web/staff/)) and any other admin or operator panel. They do **not** apply
> to the buyer-facing app ([`web/buyer`](../web/buyer/)), which keeps its own brand look
> (see [`web/buyer/brand`](../web/buyer/brand/)). Do not port Material, the gutter rules or
> the shared staff components into buyer pages.

The staff app is built in **pure Material UI** (MUI v9, Emotion). This document is the
contract for it: read it before adding or changing a page.

Paths below are relative to `web/staff/`.
Key files: [`theme/theme.js`](../web/staff/theme/theme.js) ·
[`components/Providers.jsx`](../web/staff/components/Providers.jsx) ·
[`components/StaffShell.jsx`](../web/staff/components/StaffShell.jsx)

---

## 1. Layout: fixed left/right gutter

**Pages are full width with a fixed left and right gutter. They are never centred inside a
max-width column.**

| Breakpoint | Gutter |
|---|---|
| `xs` (< 600px) | **16px** (`p: 2`) |
| `sm` and up (≥ 600px) | **32px** (`p: 4`) |

Use [`<PageContainer>`](../web/staff/components/PageContainer.jsx) as the outermost element of
every page. It applies the gutter (widened for the notch in landscape, since the viewport
extends under it), and nothing else should.

```jsx
export function SomePage() {
  return (
    <PageContainer>
      <PageHeader title="Some Page" subtitle="What this page is for." />
      {/* content */}
    </PageContainer>
  );
}
```

**Why full width, not centred.** A centred column strands content in the middle of a wide
monitor with large dead margins on both sides, and as soon as one page fills the screen and
its neighbour does not, moving between sections looks like two different apps. The gutter is
the constant and the content takes whatever width is left.

`PageContainer` accepts a `maxWidth` prop as an opt-in for genuinely narrow content. It is an
exception that needs a reason, not a default. Today only the sign-in and invitation cards
(`AuthCard`) use it.

---

## 2. Theme: Material, at an M3 tone

The theme is Material (MUI's default blue/purple palette, Onest self-hosted through
`@fontsource-variable/onest`), tuned to a **Material 3** tone in
[`theme/theme.js`](../web/staff/theme/theme.js): a larger shape scale, tonal surfaces instead
of grey hairlines, and pill controls.

**Why M3 rather than stock M2.** The staff app is tables and chips end to end. M2's 4px radius
and 1px dividers give every one of those the same weight, so a page reads as a wall of boxes.
M3 carries structure in **shape and tone** (a 16px card, a tinted header row, a filled chip),
which lets the eye find a row without a border around everything, and reflows far better when
the viewport narrows.

What that means in practice, all of it a **default** you can override per component:

| Element | Default |
|---|---|
| Radius | 12px (`shape.borderRadius`); cards and outlined paper 16, dialogs 28, buttons/nav items full |
| Buttons | `textTransform: none` (no SHOUTING labels) and `disableElevation` |
| Chips | Tonal fill over their palette colour (`primary` for `default`), not grey |
| Tabs | **`variant="scrollable"` by default.** A page has to opt *out* to clip its tabs |
| Table head | Tinted background instead of a heavy rule; survives horizontal scroll |
| Dialogs | Reclaim their margin below `sm`, so a phone dialog is not a 310px sliver |

**Colour scheme.** The theme uses CSS variables with `colorSchemeSelector: 'media'`, so light
and dark follow `prefers-color-scheme` with no script. The app is a static export, and a scheme
chosen in JavaScript would flash on first paint.

Nothing in `theme.js` hard-codes a colour: the tonal fills are `theme.alpha()` over palette
keys (`tonal()` in that file), so they follow the palette in both schemes.

Anything added to `theme.js` should be a considered deviation from Material, not a new bespoke
look.

**Never hard-code a colour.** Use palette keys so a value can only ever be a theme value:

```jsx
// Good
<Box sx={{ color: 'primary.main', bgcolor: 'background.paper' }} />
<Typography color="text.secondary" />

// Bad
<Box sx={{ color: '#0b6e4f', bgcolor: '#fff' }} />
```

The gate scanner's verdict panels follow the same rule: `success.main`, `warning.main` and
`error.main` with their `contrastText`, and `grey.800` for "Unconfirmed". The verdict is always
carried by colour, icon, words and sound together, never by colour alone.

**Charts.** There are none yet. When one is needed, use MUI X Charts (`@mui/x-charts`), which
reads the theme palette for axes, gridlines and tooltips. When colour encodes a *category*
rather than a status, do not use the semantic palette: painting categories as
`success`/`error` implies a judgement the data doesn't carry, and a theme has only about three
usable hues. Add a validated categorical palette to `theme/` with the first such chart, and
assign it in **fixed order, never cycled**, so a category keeps its colour across renders.

---

## 3. Styling rules

| Rule | Detail |
|---|---|
| **No `style={{}}`** | Use the `sx` prop. Inline styles beat every class on specificity and cannot read the theme. |
| **No CSS classes** | [`app/globals.css`](../web/staff/app/globals.css) holds only the reduced-motion rule. Everything else is theme or `sx`. |
| **No raw elements for controls** | `Button`/`IconButton`, `TextField` (with `select` for dropdowns), `Table*`, `Dialog`, `Alert`. |
| **Native selects** | Pass `slotProps={{ select: { native: true } }}` to a `TextField select`. Phones at the gate get the system picker. |
| **Icons from `components/icons.jsx`** | Hugeicons under their Material names (`import { Menu as MenuIcon } from './icons'`). `@mui/icons-material` is not installed. A missing name is added to that file. |
| **Spacing via the theme scale** | `p: 2`, `gap: 3`, not `padding: '16px'`. One unit = 8px. |
| **Responsive via breakpoint objects** | `sx={{ display: { xs: 'none', md: 'block' } }}`, not a JS media-query hook. |
| **Wide tables get a card fallback** | See §7. A table with more than ~4 columns is unusable below `md`. |

---

## 4. Shared components

All in [`components/`](../web/staff/components/). Use one when it fits; do not force it.

| Component | File | Use for |
|---|---|---|
| `PageContainer` | `PageContainer.jsx` | Outermost wrapper of every page. Always. |
| `PageHeader` | `PageHeader.jsx` | Page title, optional subtitle, optional action buttons (§6). |
| `StatCard`, `StatGrid` | `StatCard.jsx` | A single headline metric, and the grid that lays them out. |
| `StatusChip` | `StatusChip.jsx` | Status pills. Maps an API status string onto the semantic palette. |
| `ConfirmDialog` | `ConfirmDialog.jsx` | Confirmation for a consequential action. The dialog is a form, so required fields inside it are validated. Never `window.confirm`. |
| `ReasonAction` | `ReasonAction.jsx` | A staff action that needs a reason for the audit log (`ConfirmDialog` + reason field). Pass `destructive` for refunds, voids and revocations. |
| `EmptyState`, `LoadingState` | `States.jsx` | A panel that resolved to nothing / is still fetching. |
| `DesktopOnly`, `MobileOnly`, `RecordCard` | `RecordList.jsx` | The wide-table pattern (§7). |
| `AuthCard` | `SignIn.jsx` | The narrow, chrome-free card for sign-in and invitation pages. |

**When not to reach for one.** MUI is already the shared layer, so do not wrap a MUI component
just to rename its props. Two cases that look like they want a shared component but don't:

- **Modals.** Use `<Dialog>` directly. Only confirm-with-reason is common enough to justify
  `ConfirmDialog`.
- **Empty table rows.** `EmptyState` cannot live inside a `<table>`. Use a `TableRow` with a
  spanning `TableCell`.

Add a component here once the same pattern appears in **three or more** pages. Below that,
keep it local to the page: a wrong abstraction is more expensive than a repeated block.

---

## 5. Adding a page

1. Add `app/<route>/page.jsx` that renders `<StaffShell roles={[…]}>{(me) => <Screen me={me} />}</StaffShell>`,
   and add the route to `PAGES` in `StaffShell.jsx` with its roles and icon.
2. The screen component starts with `PageContainer` + `PageHeader`.
3. Loading → `LoadingState`; errors → `<Alert severity="error">`; nothing to show → `EmptyState`.
4. Statuses → `StatusChip`; audited actions → `ReasonAction`.
5. Anything wider than ~4 columns → §7.
6. Tests render through [`test/render.jsx`](../web/staff/test/render.jsx), which wraps the app
   theme. The test DOM is desktop width (1024px), so `md`-and-up branches are the visible ones.
   The 100% coverage gate still applies.
7. `make web-lint web-test web-build` must pass.

Verify the page at a narrow width too. The navigation switches to a temporary drawer below the
`md` breakpoint (900px).

---

## 6. The page header lives in the app bar

`PageHeader`'s API is `<PageHeader title subtitle actions />`. On `md` and up it **portals into
the top app bar** (the `PageHeaderSlot` node in `StaffShell`), so the chrome carries the page's
identity and the content area starts with content. Below `md` it renders in place, since the
mobile bar already holds the menu button and the account control. Outside the shell (in tests)
it renders in place.

**Every page inside the shell uses it.** The exceptions are the pages with no chrome at all,
`SignIn` and `InviteAccept`: no drawer, no app bar, nothing to portal into. They title
themselves through `AuthCard`.

If you add a page, use `PageHeader`. A page that draws its own `<Typography variant="h5">`
title puts it in the body while every neighbour puts it in the bar, and the app reads as two.

Two consequences worth knowing:

- `actions` is **mounted twice** (once per rendering, CSS shows one). Buttons are fine (the
  event editor's Publish is one); do not put uncontrolled inputs or anything holding focus in
  there.
- It is a portal, not context state. The context holds only the target DOM node. Storing the
  header itself in state would need a `useEffect` to publish it, and `actions` is a fresh
  element every render, so it would either loop forever in the dependency array or go stale the
  moment a button's `disabled` changes.

---

## 7. Wide tables: table above `md`, cards below

A table with more than about four columns cannot survive a phone. It either clips or forces a
horizontal scrub.

**The pattern:** render both, and let CSS pick one. No `useMediaQuery`: that is a JS hook
making a layout decision, and it flashes the wrong one on first paint.

```jsx
<DesktopOnly>
  <TableContainer component={Paper} variant="outlined">
    <Table size="small" sx={{ minWidth: 840 }}>…</Table>
  </TableContainer>
</DesktopOnly>

<MobileOnly>
  {items.map((item) => (
    <RecordCard key={item.id} title={item.ref} badge={<StatusChip status={item.status} />} fields={[…]} />
  ))}
</MobileOnly>
```

In use on Orders (search results), Finance (needs attention), Audit log and Gates and staff.

Three rules that make it work rather than just compile:

1. **The card is not a squeezed row.** It leads with what you identify the record *by*, and
   drops the columns that only mean something side by side. Expressing the data twice is the
   deliberate cost of that.
2. **Give the desktop `Table` a `minWidth`.** It is what makes the container scroll *itself* at
   awkward widths instead of stretching the page.
3. **Cap chip lists.** Show at most three chips in a cell and a `+n` with the rest in a
   tooltip. A row of chips in one cell is what pushes a table off the screen.

**Verify it.** Not by eye alone: check the page reports no horizontal overflow at 375px:

```js
document.documentElement.scrollWidth - document.documentElement.clientWidth  // must be 0
```
