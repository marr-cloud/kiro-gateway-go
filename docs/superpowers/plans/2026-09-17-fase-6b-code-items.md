# Fase 6b — Code Items (deferrals + parked bug) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close three deferred code items from the Fase 6a backlog — dedup the two local `getModelIDForKiro` copies onto `modelresolver.GetModelIDForKiro`, wire MCP request/response debug-logging inside `CallKiroMCPAPI`, and guard `accountmanager.GetNextAccount` against a 0-account divide-by-zero panic.

**Architecture:** Three independent, single-concern changes in disjoint packages (`accountmanager`, `convertersopenai`+`convertersanthropic`, `mcptools`), each behavior-preserving at wire boundaries and each guarded by its own test or the existing golden corpus.

**Tech Stack:** Go 1.27, standard library, `internal/pyjson`, `internal/debuglogger`, `internal/debugmiddleware`, `internal/modelresolver`.

**Spec:** `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md` (the port design doc; §D1 byte-parity at wire boundaries is the binding authority). Upstream is `jwadow/kiro-gateway` @ `a5292ca`, mirrored under `.upstream/kiro/`.

## Global Constraints

- Go 1.27, `CGO_ENABLED=0`, **no new external dependencies**.
- Every new `.go` file starts with the 2-line SPDX header:
  `// SPDX-License-Identifier: AGPL-3.0-or-later` / `// Port a Go de jwadow/kiro-gateway. Ver NOTICE.`
- Every file (including tests) stays **< 400 lines**.
- **No new mutable package-level globals.**
- `os.Getenv` only in `internal/config`.
- Byte-parity at wire boundaries (§D1) via `internal/pyjson`: `Dumps` = `ensure_ascii=False`, `DumpsASCII` = Python default (`ensure_ascii=True`). `pyjson.Dumps` reformats raw bytes token-by-token and **preserves key order**.
- Session-derived IDs are compared by SHAPE (regex), never by value.
- Upstream is the binding authority over any prose: `.upstream/kiro/*` + spec > this plan > review prose.

**Coordinator ruling (recorded, applies to Task 3):** MCP debug-log output is serialized with `pyjson.Dumps` (compact, `ensure_ascii=False`, order-preserving) using the exact `[MCP REQUEST]\n` / `[MCP RESPONSE]\n` prefixes. Upstream uses `json.dumps(..., ensure_ascii=False, indent=2)`; this port diverges in **whitespace only** (single-line vs. 2-space indent). Rationale: debug-log files are local diagnostic artifacts, not §D1 wire boundaries, and no golden corpus covers their bytes; adding an indent engine to `pyjson` is disproportionate surface for zero wire benefit. Cost if wrong: a developer sees single-line JSON instead of pretty-printed — trivially reversible later. The content (JSON structure, values, `ensure_ascii=False` escaping, key order, markers) is faithful.

---

### Task 1: accountmanager — guard `GetNextAccount` against 0 accounts

**Files:**
- Modify: `internal/accountmanager/failover.go` (add an early guard at the top of `GetNextAccount`, ~lines 39-79)
- Test: `internal/accountmanager/failover_test.go` (append one test)

**Interfaces:**
- Consumes: `Manager{ accounts []*Account; stickyIdx int; clock func() time.Time; ... }`, `ExhaustedAccountsError{ LastMsg string }` (both already defined in this package).
- Produces: no signature change. `GetNextAccount(model string, exclude map[string]struct{}) (*Account, error)` keeps its contract; the 0-account case now returns `(nil, *ExhaustedAccountsError)` instead of panicking.

**Background:** With 0 accounts, the `len(m.accounts) == 1` fast path is skipped, `nextEnabledIdx(stickyIdx, [], …)` returns `(-1, nil)` (selection.go:21-23), and control reaches `lastAccountIdx := (m.stickyIdx + len(m.accounts) - 1) % len(m.accounts)` — a `% 0` integer division panic. `NewManager` initializes `accounts: make([]*Account, 0)` (manager.go:89), so 0 accounts is reachable before/without discovery. Upstream `get_next_account` (account_manager.py:645-720) returns `None` (no account) for an empty account map — it does not raise. The faithful Go idiom for "no account available" is the typed 503 `ExhaustedAccountsError`, matching the existing exhausted path (failover.go:74-78).

- [ ] **Step 1: Write the failing test**

Append to `internal/accountmanager/failover_test.go` (ensure `errors` is imported):

```go
// TestGetNextAccount_ZeroAccountsReturnsExhaustedNoPanic verifies that an
// empty account slice does not trigger the (stickyIdx+len-1) % len divide-by-
// zero panic and instead returns a typed 503, matching upstream
// get_next_account returning None for an empty account map.
func TestGetNextAccount_ZeroAccountsReturnsExhaustedNoPanic(t *testing.T) {
	m := &Manager{
		accounts:  []*Account{},
		stickyIdx: 0,
		clock:     func() time.Time { return time.Unix(0, 0) },
	}

	acc, err := m.GetNextAccount("claude-sonnet-4", nil)
	if acc != nil {
		t.Fatalf("expected nil account for 0 accounts, got %+v", acc)
	}
	var exhausted *ExhaustedAccountsError
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected *ExhaustedAccountsError, got %T: %v", err, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (panics)**

Run: `go test ./internal/accountmanager/ -run TestGetNextAccount_ZeroAccountsReturnsExhaustedNoPanic -count=1 -v`
Expected: FAIL — panic `integer divide by zero` at failover.go's `% len(m.accounts)`.

- [ ] **Step 3: Add the guard**

In `internal/accountmanager/failover.go`, inside `GetNextAccount`, immediately after `defer m.mu.RUnlock()` and **before** `now := m.clock()`:

```go
	// Zero-account guard: with no accounts, the len==1 fast path is skipped,
	// nextEnabledIdx returns (-1,nil), and the "last attempted" index below
	// would compute (stickyIdx+len-1) % len — a divide-by-zero panic. Upstream
	// get_next_account (account_manager.py:645-720) returns None for an empty
	// account map; the Go idiom is the typed 503, same as the exhausted path.
	if len(m.accounts) == 0 {
		return nil, &ExhaustedAccountsError{LastMsg: "no accounts available"}
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/accountmanager/ -run TestGetNextAccount_ZeroAccountsReturnsExhaustedNoPanic -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Run the full package suite (no regressions)**

Run: `go test ./internal/accountmanager/ -count=1`
Expected: PASS (all existing failover/reportfailure tests unaffected).

- [ ] **Step 6: Commit**

```bash
git add internal/accountmanager/failover.go internal/accountmanager/failover_test.go
git commit -m "fix(accountmanager): guard GetNextAccount against 0-account divide-by-zero"
```

---

### Task 2: converters — dedup local `getModelIDForKiro` onto `modelresolver.GetModelIDForKiro`

**Files:**
- Modify: `internal/convertersopenai/converters.go` (swap call site; delete local model-normalization subset)
- Modify: `internal/convertersanthropic/converters.go` (same)
- Modify: `docs/MAPPING.md` (update the rows noting the duplicated subset)
- Test: no new test file — the **existing golden corpus** in `internal/convertersopenai/converters_test.go` and `internal/convertersanthropic/converters_test.go` is the byte-for-byte regression guard; `internal/modelresolver/resolver_test.go` already covers `GetModelIDForKiro` (including the hidden-model path).

**Interfaces:**
- Consumes: `modelresolver.GetModelIDForKiro(modelName string, hiddenModels map[string]string) string` (resolver.go:170). It normalizes via `modelresolver.NormalizeModelName`, looks up `hiddenModels`, and returns `ToRuntimeModelID(internal)`.
- Produces: no exported API change. Each package keeps its `var HiddenModels = map[string]string{}` (still passed as the 2nd arg and still set by that package's corpus test).

**Background (why this is byte-safe):** Upstream `get_model_id_for_kiro` (model_resolver.py:217-219) is `normalize_model_name` → `hidden_models.get(normalized, normalized)` → **`to_runtime_model_id(internal)`**. The exported `modelresolver.GetModelIDForKiro` is the faithful port (it includes the `ToRuntimeModelID` step); the two local copies omit it. `ToRuntimeModelID` is currently a pass-through identity (normalize.go:101-103), so for **every** input `modelresolver.GetModelIDForKiro(x, h)` equals the local `getModelIDForKiro(x, h)` today — the swap is byte-identical now and routes both dialects through the canonical seam for the future. The local `normalizeModelName` + the six model-normalization regex package vars are used **only** by the local `getModelIDForKiro` (verified: no other reference in either package or its tests), so they are safe to delete. `modelresolver.NormalizeModelName` (normalize.go:68) is the same literal port of the five patterns.

- [ ] **Step 1: Confirm the corpus is green BEFORE any change (baseline)**

Run: `go test ./internal/convertersopenai/ ./internal/convertersanthropic/ ./internal/modelresolver/ -count=1`
Expected: PASS. This is the baseline the refactor must preserve byte-for-byte.

- [ ] **Step 2: Edit `internal/convertersopenai/converters.go`**

1. Add `"github.com/marr-cloud/kiro-gateway-go/internal/modelresolver"` to the import block.
2. At the `build_kiro_payload` call site (currently line 609), swap:
   ```go
   modelID := getModelIDForKiro(req.Model, HiddenModels)
   ```
   →
   ```go
   modelID := modelresolver.GetModelIDForKiro(req.Model, HiddenModels)
   ```
3. **Delete** these now-dead local symbols (each is referenced only by the others):
   - the `var ( modelSuffixPattern … invertedSuffixPattern )` regex block (currently ~lines 92-102),
   - the `normalizeModelName` function (currently ~lines 104-136),
   - the local `getModelIDForKiro` function (currently ~lines 138-148).
4. **Keep** `var HiddenModels = map[string]string{}` (still used at the call site and by the corpus test).
5. Trim the header comment that justified duplicating the subset (currently ~lines 60-89): replace the "duplicate the minimal subset" rationale with one short paragraph stating that model-ID resolution now delegates to `modelresolver.GetModelIDForKiro` (the literal port of `get_model_id_for_kiro`), and that `HiddenModels` mirrors `HIDDEN_MODELS`. Update the inline comment near the old call site (currently line 593) to name `modelresolver.GetModelIDForKiro`.
6. Remove any import left unused by the deletions (e.g. `regexp`; keep `strings` only if still used elsewhere in the file). Let `go build` / `goimports` decide — do not guess.

- [ ] **Step 3: Edit `internal/convertersanthropic/converters.go` (symmetric)**

Apply the identical transformation: add the `modelresolver` import; swap the call at the `build_kiro_payload` call site (currently line 720) to `modelresolver.GetModelIDForKiro(req.Model, HiddenModels)`; delete the six regex vars, `normalizeModelName`, and the local `getModelIDForKiro`; keep `HiddenModels`; trim the analogous header comment; drop now-unused imports.

- [ ] **Step 4: Build (import cycle + unused-import check)**

Run: `go build ./...`
Expected: clean. (`modelresolver` is a leaf package — it must not import either converters package; the build confirms no cycle.)

- [ ] **Step 5: Run the corpus — MUST stay byte-identical**

Run: `go test ./internal/convertersopenai/ ./internal/convertersanthropic/ ./internal/modelresolver/ -count=1`
Expected: PASS with **zero** golden diffs. This is the proof that the swap is behavior-preserving at the wire boundary (the `model_id` sent to Kiro). If any golden case diffs, STOP — `ToRuntimeModelID` or `NormalizeModelName` is not identity for that input and the swap is not safe; report it.

- [ ] **Step 6: Update `docs/MAPPING.md`**

Update the rows that describe the duplicated subset (around lines 46 and 67, which currently say the converters carry a local `normalizeModelName`/`getModelIDForKiro` + `HiddenModels` and note "Cuando internal/modelresolver exista, …") to state that both dialects now delegate to `modelresolver.GetModelIDForKiro` and retain only their `HiddenModels` package var.

- [ ] **Step 7: Full build + vet + broad test**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: all PASS (no other package depended on the deleted local symbols).

- [ ] **Step 8: Commit**

```bash
git add internal/convertersopenai/converters.go internal/convertersanthropic/converters.go docs/MAPPING.md
git commit -m "refactor(converters): delegate getModelIDForKiro to modelresolver, drop duplicated subset"
```

---

### Task 3: mcptools — wire MCP request/response debug-logging inside `CallKiroMCPAPI`

**Files:**
- Modify: `internal/debugmiddleware/middleware.go` (add exported `WithLogger` context seam; route `New`'s injection through it)
- Modify: `internal/mcptools/client.go` (log request + response via `FromContext(ctx)`)
- Modify: `docs/MAPPING.md` (record the `mcp_tools.py:139-145,169-175` logging rows + the whitespace-divergence ruling)
- Test: `internal/mcptools/client_test.go` (add a positive logging test + rely on existing no-logger tests for the nil path)

**Interfaces:**
- Consumes: `debugmiddleware.FromContext(ctx) *debuglogger.DebugLogger` (nil-safe; middleware.go:38), `debuglogger.DebugLogger.LogRawChunk([]byte)` (debuglogger.go:110), `pyjson.Dumps(json.RawMessage) (string, error)`.
- Produces: `debugmiddleware.WithLogger(ctx context.Context, logger *debuglogger.DebugLogger) context.Context` (new exported helper storing under the existing unexported `loggerCtxKey`). `CallKiroMCPAPI`'s signature is **unchanged** — the logger is read from `ctx`.

**Background (fidelity):** Upstream `call_kiro_mcp_api(query, auth_manager)` takes no `debug_logger` parameter; it references the process-global `debug_logger` singleton at mcp_tools.py:139-145 (log request, before `get_access_token`) and :169-175 (log response, after `response.json()`, before the `error` check). The Go port has no such global (fase-6a ruling), so `CallKiroMCPAPI` retrieves the request-scoped logger from `ctx` via `FromContext` — same effect, no global, no signature change. `HandleNativeWebSearch` already passes `ctx` down (websearch.go:144). Existing `client_test.go` calls pass `context.Background()` → `FromContext` returns nil → logging is skipped → their behavior is unchanged (this covers the nil-logger path). Serialization uses `pyjson.Dumps` per the Global-Constraints ruling.

**Import-cycle note:** `mcptools` → `debugmiddleware` → {`config`, `debuglogger`} is acyclic (neither `debugmiddleware`, `config`, nor `debuglogger` imports `mcptools`). Confirm with `go build ./...`.

- [ ] **Step 1: Add the `WithLogger` seam to `internal/debugmiddleware/middleware.go`**

Add (near `FromContext`):

```go
// WithLogger devuelve un context con logger instalado bajo loggerCtxKey — la
// misma clave que lee FromContext. Exportado para que llamadores fuera de este
// paquete (p.ej. tests de mcptools) inyecten el logger igual que lo hace New.
func WithLogger(ctx context.Context, logger *debuglogger.DebugLogger) context.Context {
	return context.WithValue(ctx, loggerCtxKey{}, logger)
}
```

Then route `New`'s injection through it — replace (currently line 61):
```go
			ctx := context.WithValue(r.Context(), loggerCtxKey{}, logger)
```
→
```go
			ctx := WithLogger(r.Context(), logger)
```

- [ ] **Step 2: Write the failing logging test**

Append to `internal/mcptools/client_test.go`. It reuses the existing double-deserialize fixture shape (a fake `/mcp` server returning `result.content[0].text` = a JSON string), injects an `ModeAll` logger writing to a temp dir, and asserts both markers + payloads land in `response_stream_raw.txt`:

```go
// TestCallKiroMCPAPI_DebugLoggingWritesRequestAndResponse verifies that when a
// DebugLogger is present in ctx, CallKiroMCPAPI logs the MCP request and the
// full MCP response via LogRawChunk with the [MCP REQUEST]/[MCP RESPONSE]
// markers (mcp_tools.py:139-145,169-175). ModeAll writes immediately to disk.
func TestCallKiroMCPAPI_DebugLoggingWritesRequestAndResponse(t *testing.T) {
	inner := `{"results":[{"title":"T<>&"}],"totalResults":1}`
	envelope := map[string]any{
		"id":      "resp-1",
		"jsonrpc": "2.0",
		"result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": inner}},
			"isError": false,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dl := debuglogger.New(debuglogger.ModeAll, dir)
	ctx := debuglogger.NewRequestContextForTest(dl, debugmiddleware.WithLogger(context.Background(), dl))

	_, _, err := CallKiroMCPAPI(ctx, srv.URL, "golang <b>", fakeTokenProvider{token: "t"})
	if err != nil {
		t.Fatalf("CallKiroMCPAPI returned unexpected error: %v", err)
	}

	raw, rerr := os.ReadFile(filepath.Join(dir, "response_stream_raw.txt"))
	if rerr != nil {
		t.Fatalf("reading response_stream_raw.txt: %v", rerr)
	}
	got := string(raw)
	for _, want := range []string{
		"[MCP REQUEST]\n",
		"[MCP RESPONSE]\n",
		`"query": "golang <b>"`, // ensure_ascii=False: '<' '>' NOT HTML-escaped
		`"totalResults": 1`,     // response re-dumped, key order preserved
	} {
		if !strings.Contains(got, want) {
			t.Errorf("response_stream_raw.txt missing %q; got:\n%s", want, got)
		}
	}
}
```

**Note on `ModeAll` prerequisites:** `LogRawChunk` only writes when the logger `isEnabled()` AND, in `ModeAll`, `appendToFile` needs `DEBUG_DIR` to exist. In the real flow `PrepareNewRequest` recreates the dir. The test must reproduce that precondition. If `debuglogger` has no existing exported test helper to prepare a request, the implementer must create the minimal one it needs rather than inventing `NewRequestContextForTest` blindly:
- Prefer calling the real entry point: `ctx = dl.PrepareNewRequest(debugmiddleware.WithLogger(context.Background(), dl))` (PrepareNewRequest recreates the dir in ModeAll and returns the ctx). Replace the `NewRequestContextForTest` line above with this. Verify `response_stream_raw.txt` ends up under `dir`.
- If a directory still isn't present, `os.MkdirAll(dir, 0o755)` in the test before the call is an acceptable fallback.

The implementer owns making this test genuinely exercise the write path; the assertions above are the contract.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/mcptools/ -run TestCallKiroMCPAPI_DebugLoggingWritesRequestAndResponse -count=1 -v`
Expected: FAIL — no `[MCP REQUEST]`/`[MCP RESPONSE]` written (logging not yet wired).

- [ ] **Step 4: Wire logging in `internal/mcptools/client.go`**

Add imports: `"github.com/marr-cloud/kiro-gateway-go/internal/debugmiddleware"` and `"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"`.

Inside `CallKiroMCPAPI`, retrieve the logger once near the top:
```go
	logger := debugmiddleware.FromContext(ctx)
```

Log the **request** right after `body, err := json.Marshal(mcpRequest)` succeeds and **before** `tp.AccessToken` (mirroring upstream ordering — request logged before token fetch):
```go
	// Log MCP request (mcp_tools.py:139-145). pyjson.Dumps re-emits body with
	// ensure_ascii=False (undoing encoding/json's HTML-escaping of <,>,&) and
	// preserves key order; struct field order matches the upstream dict.
	if logger != nil {
		if reqDump, derr := pyjson.Dumps(body); derr == nil {
			logger.LogRawChunk([]byte("[MCP REQUEST]\n" + reqDump))
		}
	}
```

Log the **response** right after `json.Unmarshal(respBody, &envelope)` succeeds and **before** the `isJSONAbsentOrNull(envelope.Error)` check (mirroring upstream: response logged after parse, before the error branch, so error responses are logged too):
```go
	// Log MCP response (mcp_tools.py:169-175). Re-dump the raw response bytes
	// with ensure_ascii=False, order preserved.
	if logger != nil {
		if respDump, derr := pyjson.Dumps(respBody); derr == nil {
			logger.LogRawChunk([]byte("[MCP RESPONSE]\n" + respDump))
		}
	}
```

Both blocks are nil-safe (skip when no logger) and swallow a `pyjson.Dumps` error without failing the call — matching upstream's `try/except: logger.warning(...)` that never aborts the MCP call on a logging failure.

- [ ] **Step 5: Run the new test to verify it passes**

Run: `go test ./internal/mcptools/ -run TestCallKiroMCPAPI_DebugLoggingWritesRequestAndResponse -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Run the whole mcptools + debugmiddleware suites (no regressions)**

Run: `go test ./internal/mcptools/ ./internal/debugmiddleware/ -count=1`
Expected: PASS — existing `CallKiroMCPAPI` tests (which pass `context.Background()`, nil logger) behave identically; `debugmiddleware` tests unaffected by the `WithLogger` extraction.

- [ ] **Step 7: Update `docs/MAPPING.md`**

Add/adjust the `mcp_tools.py` rows to note that `call_kiro_mcp_api`'s request/response logging (`:139-145`, `:169-175`) is ported inside `CallKiroMCPAPI` reading the request-scoped `*debuglogger.DebugLogger` from `ctx` (no process global), and that the debug-log serialization uses `pyjson.Dumps` (compact) — a whitespace-only divergence from upstream's `indent=2`, per the plan's Global-Constraints ruling.

- [ ] **Step 8: Full build + vet + broad test**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: all PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/debugmiddleware/middleware.go internal/mcptools/client.go internal/mcptools/client_test.go docs/MAPPING.md
git commit -m "feat(mcptools): log MCP request/response to the request-scoped debug logger"
```

---

## Self-Review

**1. Spec coverage:** The three backlog code items are each a task. The fourth deferral — wire streaming `200000` → `cache.GetMaxInputTokens` — is intentionally excluded: it needs dynamic model discovery and stays parked (noted in memory + ledger).

**2. Placeholder scan:** No TBD/TODO. Task 1 and Task 3 carry full test + implementation code. Task 2 is a behavior-preserving refactor whose regression guard is the existing golden corpus (Step 5), which is the correct test surface for a dedup; deletions are specified by symbol (robust to line drift) with current line pointers.

**3. Type consistency:** `modelresolver.GetModelIDForKiro(string, map[string]string) string` matches the local signature it replaces (Task 2). `debugmiddleware.WithLogger(context.Context, *debuglogger.DebugLogger) context.Context` and `FromContext(context.Context) *debuglogger.DebugLogger` are duals over the same `loggerCtxKey` (Task 3). `ExhaustedAccountsError{LastMsg string}` and `Manager` fields match `failover.go`/`manager.go` (Task 1). `pyjson.Dumps(json.RawMessage) (string, error)` matches the call (`body`, `respBody` are `[]byte`, assignable to `json.RawMessage`).
