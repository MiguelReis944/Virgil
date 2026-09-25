# Golden Bough Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign Virgil's complete local dashboard with the approved Golden Bough visual system while preserving every existing route, field, filter, state, and security behavior.

**Architecture:** Keep the server-rendered Go templates and dependency-free delivery model. Centralize the visual system in the shared panel shell, then make focused markup/class refinements only where a page needs stronger hierarchy; behavioral handlers and queries remain unchanged.

**Tech Stack:** Go 1.26, `html/template`, embedded CSS, `net/http`, Go tests

**Spec:** `docs/design/2026-09-25-golden-bough-dashboard.md`

## Global Constraints

- Preserve Overview, Executions, Protections, Providers, Usage, Health, and Settings.
- Preserve existing routes, filters, forms, authentication, CSRF protection, links, and responsive behavior.
- Add no remote fonts, images, scripts, frameworks, or runtime dependencies.
- Do not change database queries, policy semantics, provider handling, credentials, or persistence.
- Preserve semantic HTML, visible keyboard focus, reduced-motion support, and WCAG AA contrast.
- Do not overwrite unrelated local changes already present in the worktree.

---

### Task 1: Lock the shared shell contract

**Files:**
- Modify: `internal/dashboard/layout_test.go`
- Modify: `internal/dashboard/layout.go`

**Interfaces:**
- Consumes: existing `renderPage`, `pageData`, `productNavigation`, and page body templates.
- Produces: the shared Golden Bough token system and shell used by every dashboard page.

- [ ] **Step 1: Add failing shell assertions**

Add assertions to `TestRenderPageProvidesProductNavigationAndSecurityHeaders` for
the stable hooks `data-theme="golden-bough"`, `class="brand-mark"`,
`class="nav-status"`, `--accent:#c6a15b`, and a visible skip link targeting
`#main-content`.

- [ ] **Step 2: Run the focused test and verify failure**

Run: `go test ./internal/dashboard -run TestRenderPageProvidesProductNavigationAndSecurityHeaders -count=1`

Expected: FAIL because the Golden Bough shell hooks are not rendered yet.

- [ ] **Step 3: Replace the shared shell styles and markup**

In `panelLayout`, retain all existing template data and navigation iteration while
introducing the approved palette, typography, responsive sidebar, top bar, skip
link, `main-content` target, accessible focus states, table treatment, form
controls, badges, code blocks, and reduced-motion rule. Keep all class names used
by page templates so pages remain functional during the transition.

- [ ] **Step 4: Run the shell tests**

Run: `go test ./internal/dashboard -run 'TestRenderPage|TestAuthenticatedPanelHandlersUseOneShellContract' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the shell checkpoint**

```text
git add internal/dashboard/layout.go internal/dashboard/layout_test.go
git commit -m "redesign Virgil dashboard shell"
```

### Task 2: Refine overview and operational state hierarchy

**Files:**
- Modify: `internal/dashboard/overview.go`
- Modify: `internal/dashboard/layout_test.go`

**Interfaces:**
- Consumes: shared `.cards`, `.card`, semantic value, badge, and empty-state styles.
- Produces: an overview whose operational metrics lead without changing `overviewData` or `queryOverview`.

- [ ] **Step 1: Add a failing overview hierarchy assertion**

Extend the overview shell test to require `class="metric-grid operational-grid"`
and an overview section heading containing `Execution safety` while retaining all
four existing metric labels.

- [ ] **Step 2: Run the focused test and verify failure**

Run: `go test ./internal/dashboard -run 'TestRenderPage|TestAuthenticatedPanelHandlersUseOneShellContract' -count=1`

Expected: FAIL because the overview hierarchy hooks do not exist.

- [ ] **Step 3: Restructure only the overview markup**

Update `overviewBody` to add a concise section header and operational grid classes.
Keep provider setup guidance, metric values, conditional content, and destination
links intact.

- [ ] **Step 4: Run overview and dashboard tests**

Run: `go test ./internal/dashboard -run 'Overview|AuthenticatedPanelHandlers' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the overview checkpoint**

```text
git add internal/dashboard/overview.go internal/dashboard/layout_test.go
git commit -m "refine dashboard operational overview"
```

### Task 3: Normalize data-dense pages and forms

**Files:**
- Modify: `internal/dashboard/layout.go`
- Modify: `internal/dashboard/handler.go`
- Modify: `internal/dashboard/executions.go`
- Modify: `internal/dashboard/protections.go`
- Modify: `internal/dashboard/providers.go`
- Modify: `internal/dashboard/health.go`
- Modify: `internal/dashboard/settings.go`
- Test: `internal/dashboard/layout_test.go`
- Test: existing focused tests beside every modified handler

**Interfaces:**
- Consumes: existing handler data structures, form names, actions, CSRF token, query parameters, and table data.
- Produces: consistent `.section-head`, `.filter-bar`, `.data-panel`, `.form-section`, `.diagnostic-list`, and `.code-panel` presentation hooks.

- [ ] **Step 1: Add failing preservation and presentation assertions**

Add shell-contract checks for the new structural hooks while retaining assertions
for all navigation labels. In focused page tests, assert existing form field names,
filters, CSRF tokens, command examples, state labels, and links remain rendered.

- [ ] **Step 2: Run all dashboard tests and verify only new assertions fail**

Run: `go test ./internal/dashboard -count=1`

Expected: FAIL on missing presentation hooks; existing behavior assertions remain
green.

- [ ] **Step 3: Add shared structural styles**

Extend `panelLayout` with the six structural classes, responsive table containers,
form grouping, diagnostic rows, timeline styling, and compact explanatory panels.
Do not add scripts or external assets.

- [ ] **Step 4: Apply structural classes page by page**

Edit template strings only: group existing controls and information without
renaming inputs, changing conditions, removing values, or modifying HTTP handlers.
Keep `handler.go` changes limited to usage/session/event/run markup and its local
styles.

- [ ] **Step 5: Run the complete dashboard package tests**

Run: `go test ./internal/dashboard -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the complete page checkpoint**

```text
git add internal/dashboard
git commit -m "apply Golden Bough dashboard system"
```

### Task 4: Verify responsive, security, and repository behavior

**Files:**
- Modify only if verification exposes a regression in files already listed above.

**Interfaces:**
- Consumes: completed server-rendered dashboard.
- Produces: evidence that the redesign is safe, responsive, and behavior-preserving.

- [ ] **Step 1: Run formatting**

Run: `gofmt -w internal/dashboard/*.go`

Expected: modified Go sources are formatted without changing behavior.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`

Expected: PASS, with database-dependent tests allowed to skip only under their
existing documented conditions.

- [ ] **Step 3: Check the diff for behavior drift and whitespace errors**

Run: `git diff --check`

Expected: no output.

Run: `git diff -- internal/dashboard`

Expected: presentation and test changes only; no route, query, authentication,
CSRF, credential, or persistence changes.

- [ ] **Step 4: Verify rendered pages at desktop and narrow widths**

Start Virgil with the existing local development configuration and inspect
Overview, Executions, Protections, Providers, Usage, Health, and Settings at a
desktop viewport and a viewport at or below 760 px. Confirm navigation, table
scrolling, keyboard focus, form controls, code wrapping, and semantic state labels.

- [ ] **Step 5: Commit verification fixes if any were required**

```text
git add internal/dashboard
git commit -m "polish responsive dashboard details"
```

Do not create an empty commit when verification required no changes.
