# Retired React test carrier references

Status: the maintainer explicitly approved this retirement on 2026-11-04 after
reviewing the automatic-review objection. The ordinary React source and old
executable scripts are retired; the original assertions remain here as reference.
Their old generated binding/runtime dependencies are absent from the product. These
files preserve the original assertions for migration review; they are excluded
from the built application. `assertions.json` records the old browser checks.
The old mocked runtime and binding interception are replaced by the Native
entry points below. Coverage is representative of the supported behavior, not a
claim of one-to-one coverage of every historical DOM assertion.

The retired DesktopService close handshake is also retained as `.reference`;
`internal/desktop/close_test.go` and the real-window close/cancel probe cover the
Native replacement. User project files and browser profiles are not removed.

Current replacements:

- Native product controls: `frontend/e2e/product.mjs` runs
  `internal/desktop/native_product_test.go`, composer, property, Arrangement,
  bridge, time-map and recovery tests against Native widgets/real app services.
- Real desktop editor integration: `frontend/e2e/native.mjs` builds and runs
  `internal/desktop/native_test.go`, the actual MyGo/WebKit/local-model flow,
  1k/10k offscreen selection/transforms, shared undo and interrupted edit checks.
- The former Go/Chromium test transport is replaced by
  `internal/app/project_native_test.go`; domain permission/scope/approval/atomic
  publication tests remain in `internal/app` and `internal/storage`.
- Musical projection, tempo/meter and GM metadata tests remain in
  `frontend/tests` and continue running independently of the UI framework.

These are behavioral replacements, not an assertion that every historical DOM
selector or every physical-device scenario has been tested. Exact DOM style
checks on retired controls are no longer applicable. IME/device/DPI and full
frame-p95 acceptance remain separately identified in the migration acceptance
record; a headless Native control test does not establish those outcomes.
