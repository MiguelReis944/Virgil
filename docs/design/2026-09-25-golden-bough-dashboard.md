# Golden Bough Dashboard Design

## Objective

Make Virgil's local dashboard feel precise, trustworthy, and production-ready
without removing or weakening any existing mechanism. The redesign covers the
shared shell and every existing page: Overview, Executions, Protections,
Providers, Usage, Health, and Settings.

## Product idea

Virgil protects supervised AI executions with deterministic limits. The visual
system should therefore communicate control, legibility, and safe passage rather
than spectacle.

The concept is **Golden Bough**. In Virgil's *Aeneid*, the golden bough grants
passage into a dangerous, obscured place. In this interface it becomes a restrained
signal for guidance: selected navigation, keyboard focus, active protection, and
primary actions. The reference stays conceptual; the interface must not use Roman
columns, statues, laurel ornament, Latin copy, or imperial imagery.

## Design principles

1. **State before decoration.** Color, borders, and density explain system state.
2. **One hierarchy.** Page title, primary content, supporting content, then detail.
3. **Quiet surfaces.** Avoid glow, broad gradients, and diffuse shadows.
4. **Dense where useful.** Tables and timelines favor scanning over oversized cards.
5. **Local-first clarity.** The shell always communicates that Virgil is protecting
   a local runtime.
6. **No behavior loss.** Existing routes, filters, form fields, authentication,
   CSRF protection, links, tables, and responsive behavior remain available.

## Palette

| Token | Value | Use |
| --- | --- | --- |
| `--bg` | `#08090a` | Application background, Avernus |
| `--sidebar` | `#0d0e10` | Navigation and top chrome |
| `--surface` | `#141518` | Cards, tables, form sections |
| `--surface-raised` | `#1a1b1f` | Hover and nested surfaces |
| `--border` | `#292b30` | Primary dividers |
| `--border-subtle` | `#202226` | Quiet separators |
| `--text` | `#f2f0e9` | Primary text, parchment |
| `--text-dim` | `#b2b1ad` | Supporting text |
| `--text-muted` | `#7e7f85` | Metadata |
| `--accent` | `#c6a15b` | Guidance and primary focus |
| `--accent-strong` | `#dfbd76` | Accent hover and active text |
| `--success` | `#48b88a` | Healthy and permitted states |
| `--warning` | `#d99a4e` | Attention states |
| `--danger` | `#e06c75` | Blocks and failures |

The accent is not a chart-series default. Data visualization should use cool
neutral blues and slate tones, reserving semantic colors for outcomes.

## Typography

- Use a system-first stack compatible with the embedded, dependency-free panel:
  `Inter`, `Geist`, `Segoe UI`, sans-serif.
- Use `ui-monospace`, `SFMono-Regular`, `Cascadia Code`, monospace for IDs,
  commands, durations, costs, tokens, and tabular metrics.
- Use sentence case for navigation and labels.
- Do not introduce an external font request; the dashboard must remain usable
  offline.

## Layout

- Fixed desktop sidebar: 224 px.
- Content width: fluid with a 1440 px maximum and 24–32 px page gutters.
- Shared top bar: compact title on the left and page actions on the right.
- Twelve-column mental grid, implemented with responsive CSS grids.
- Cards may span different widths according to importance; avoid a uniform wall
  of equivalent tiles.
- At 760 px and below, navigation becomes a horizontally scrollable compact rail
  above the content. Data tables retain horizontal scrolling rather than hiding
  columns.

## Shared component language

- Cards use an 8 px radius, one-pixel border, and no default shadow.
- Buttons use a 7 px radius and 36 px minimum height. Primary buttons use the
  parchment foreground on the gold accent; secondary buttons remain dark.
- Inputs have persistent borders, clear labels, and a gold focus ring.
- Status badges combine text, color, and a dot or border so color is never the
  only signal.
- Tables use sticky headers where practical, tabular numerals, subtle row hover,
  and stronger contrast for the primary identifier.
- Code blocks use a raised black surface, wrap safely, and remain selectable.
- Motion is limited to 120–160 ms state transitions and is disabled through
  `prefers-reduced-motion`.

## Page hierarchy

### Overview

Lead with the operational condition: active executions, blocked executions,
successful circuit breaks, and termination failures. Keep setup guidance visible
when no provider exists, then show supporting activity.

### Executions

Make the filter bar and execution table the dominant elements. Keep every filter,
state label, metric, and detail link. The detail page reads as a lifecycle timeline
with policy and termination outcomes visually separated.

### Protections

Group existing controls by budget, volume, duration, and allowlists. Preserve every
field and the current save/restart semantics. Explanatory copy becomes a compact
onboarding panel instead of competing with the form.

### Providers

Separate provider identity, endpoint/model, credentials, and protocol capabilities.
Keep every current form value, CSRF field, command example, and restart message.

### Usage

Order content as summary metrics, time series, distribution, sessions/runs, and
event history. Preserve filters, tabs, links, and all data fields.

### Health

Present checks as a diagnostic list with clear healthy, warning, and failure
states. Applied configuration and pending restart state must remain distinct.

### Settings

Present applied configuration as read-only operational facts. Pending settings
must be visibly marked without exposing secrets or filesystem paths.

## Accessibility and quality constraints

- Meet WCAG AA contrast for body text and interactive controls.
- Keep visible `:focus-visible` styling.
- Preserve semantic headings, labels, tables, navigation landmarks, and status
  roles already rendered by the Go templates.
- Keep the Content Security Policy compatible with an embedded stylesheet; do not
  add remote assets or client-side dependencies.
- Verify at desktop and narrow viewport widths.
- Existing dashboard tests must continue to pass, and shell tests should assert
  the new design tokens and accessible navigation contract.

## Scope boundary

This work changes presentation and information hierarchy only. It does not change
database queries, route behavior, policy semantics, provider handling, credentials,
authentication, CSRF, or persistence.
