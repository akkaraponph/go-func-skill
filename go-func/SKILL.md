---
name: go-func
description: Go monolith conventions built on three function types (Pure Logic, Side Effect, Orchestration) and Ports & Adapters, in a Simple profile (flat package per domain) or an Advanced profile (domain/port/application/adapter, domain events, append-only repositories). Use when adding a Go use case, repository, handler, subscriber, or worker; wiring a service; deciding where a Go function or file belongs; reviewing a Go domain module; or graduating a domain between profiles.
license: MIT
---

# Go Func — Hexagonal Convention

Business logic isolated from infrastructure, with every function classified. Most decisions reduce to: which of the three function types is this, and where does it live?

## Work in this order

**Writing code**

1. **Find the profile** using the table below. For an Advanced domain, read [`references/advanced.md`](references/advanced.md) in full before writing.
2. **Classify** every function you are about to add as Pure Logic, Side Effect, or Orchestration. Done when each has exactly one type.
3. **Write Pure Logic first**, with a table test. For the shape of a complete use case, read [`references/example.md`](references/example.md).
4. **Write the Side Effects and the Orchestration**, then wire them in `cmd/<app>/main.go`.
5. **Verify** from the module root. Done when `go run <this skill's directory>/scripts/archcheck.go` prints `archcheck: clean`, `go test -race ./...` passes, and every line of the review checklist is true.

**Reviewing code**

1. Run archcheck and `go test -race ./...`.
2. Apply the review checklist below, plus the Advanced checklist for Advanced domains.

Done when every checklist line has a pass or a failure with its `file:line`.

## Choose a profile

Pick the lightest profile the codebase can live with. Graduate one domain at a time; [`references/advanced.md`](references/advanced.md) has the steps.

| | **Simple** | **Advanced** |
|---|---|---|
| Best for | small / early / few-domain monolith | large monolith, many domains, external integrations |
| Structure | one flat package per domain, **files not folders** | per-domain `domain/port/application/adapter` folders |
| Service → store | service calls the repository **directly** | writes go out as events; subscribers persist |
| Repositories | normal CRUD is fine | append-only (`Create*` and reads) |
| Interfaces (ports) | only where you need a seam (test/swap) | every service has a port + compile-time check |

Subfolders like `port/` and `adapter/` mean Advanced. Flat `*.go` files in a domain package mean Simple.

Rules below apply to both profiles unless tagged **[simple]** or **[advanced]**.

## The three function types

**Every function is exactly one of three types.** Simple keeps the same discipline without the folders.

| Type | Does | Takes `ctx` | Lives in (Advanced) | Returns |
|------|------|------|----------|---------|
| **Pure Logic** | No I/O, no side effects, deterministic | no | `domain/`, `application/builders/`, `application/mappers/` | plain `T`, or `error` for validation |
| **Side Effect** | DB, network, filesystem, queues, clock, randomness | yes | `adapter/` | the codebase's error convention |
| **Orchestration** | Composes Pure + Side Effect, runs a use case | yes | `application/` (Service methods) | the codebase's error convention |

In **Simple**, the three types live as clearly named functions in one package: `validate*` and `build*` are Pure, `*Repository` methods are Side Effect, `Service` methods are Orchestration.

- **Time and IDs are inputs to Pure Logic.** `time.Now()`, `rand`, and `uuid.New()` are Side Effects, so `build*` takes `now time.Time` and `id string` as parameters. The service gets them from injected `now func() time.Time` and `newID func() string`, set at wiring time. Tests pass fixed values.
- Pure validation that returns an `error` is still Pure Logic.
- Adapters and repositories stay thin: translate, store, return. Business rules belong in Pure Logic.
- A handler is a Side Effect: decode, call one use case, encode.

## Layout — [simple]

One package per domain, flat files. Add a folder only when a single file gets unwieldy. For Advanced, see [`references/advanced.md`](references/advanced.md).

```
internal/<domain>/
├── model.go        — structs, constants, errors, validate*/build*  (Pure Logic)
├── service.go      — use-case methods                              (Orchestration)
├── repository.go   — storage access                                (Side Effect)
├── handler.go      — HTTP transport                                (Side Effect)
└── port.go         — interface(s), only where you need a seam
```

`main.go` is the only place that names concrete implementations. Open connections there at startup so bad configuration fails before the first request; for config and credential loading, load `safety-first`.

## Ports

- Name use-case methods after business actions: `PlaceOrder`, `CancelOrder`, `ViewOrderByID`, rather than `Create`, `Get`, `Update`. This applies to use cases; repository methods may use storage verbs.
- Define an interface in the package that consumes it. **[simple]** Add one only where you need a seam to fake in a test or swap an implementation.
- **[simple]** Hard deletes and schema migrations follow `safety-first`.

## Context

`ctx context.Context` is the first parameter of every Side Effect and Orchestration function, and is passed all the way through. Pure Logic takes none, so a `ctx` parameter marks a function as not Pure. Work that must outlive the request starts from `context.WithoutCancel(ctx)`.

## Errors

**One convention per codebase:** plain `(T, error)`, or `Result[T]` / `Option[T]` (e.g. `github.com/samber/mo`). A new Advanced codebase defaults to `Result`. A codebase graduating from Simple keeps what it has, so domains never disagree.

- Domain errors are sentinels in the domain's Pure code: `ErrNotFound`, `ErrOutOfStock`.
- Adapters translate storage errors into domain errors, so `sql.ErrNoRows` stops at the repository.
- Add context with `fmt.Errorf("place order: %w", err)` so `errors.Is` still works.
- One function maps domain errors to transport status. Unknown errors are logged and returned as a generic 500, so internals stay out of responses.

## Transactions

The transaction boundary sits in Orchestration, and repositories join it through `ctx`:

```go
type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
```

- Do reads and external calls first, then open the transaction around the writes only.
- **[advanced]** Services hold no transactions; the save subscriber owns the boundary.
- [`references/example.md`](references/example.md) has the storage-agnostic implementation.

## Cross-domain communication

Domains reach each other only through these channels:

1. **Injected port interface** — define it in your domain, inject the implementation at wiring time. Best for synchronous queries.
2. **Domain events** — publish; the other domain subscribes. Best for notifications, cascades, and audit. **[advanced]**
3. **`pkg/`** — for infrastructure utilities with no domain knowledge.

A use case that must change two domains atomically is a sign the boundary is wrong. Raise it rather than sharing a transaction across domains.

## Background workers

- Accept a `context.Context` and exit on `ctx.Done()`.
- Use bounded queues, and decide what happens when one is full.
- Every goroutine has an owner that waits for it during shutdown.

## Testing shape

- **Pure Logic** — table test with no fakes.
- **[simple] Service** — fake repository and fixed clock. Assert the result, the writes, and that a failure stops later steps.
- **[advanced] Service** — assert the event was published, including that a `Publish` error reaches the caller.
- **[advanced] Subscriber** — assert the repository call. Deliver the same event twice to prove asynchronous subscribers are idempotent.
- **Repository** — against a real test database, because a fake cannot prove a query. For append-only: original and amendment both exist; tombstone created, original intact.
- A fake used by many tests earns a shared contract test, run against both the fake and the real adapter so they cannot drift.

## Review checklist

`archcheck` reports mechanical violations. Review the remaining behavior by reading the code and tests. Advanced domains also use the checklist in [`references/advanced.md`](references/advanced.md).

- [ ] The profile is clear and consistent within each domain
- [ ] Every function is one clear type
- [ ] Pure Logic has no clock, randomness, context, or I/O *(`archcheck` checks selected imports and calls in `domain/`, `builders/`, `mappers/`, and Simple `model.go`)*
- [ ] No business logic in repositories or adapters
- [ ] Use-case methods named by business action
- [ ] One error convention throughout
- [ ] Storage errors translated at the adapter; one error-to-status mapping
- [ ] `ctx` first and passed through on every Side Effect and Orchestration function
- [ ] Multi-write use cases are transactional, with external calls outside the transaction
- [ ] No cross-domain imports of internals *(auto)*
- [ ] Workers stop on `ctx.Done()`, use bounded queues, and are awaited at shutdown
- [ ] Pure Logic has table tests; failure paths are tested
