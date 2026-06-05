---
name: go-func
description: Write and review Go code under a Ports & Adapters (hexagonal) convention with an append-only event store. Two profiles — Simple (flat package per domain, files not folders, direct repo calls) for small/early monoliths, and Advanced (full domain/port/application/adapter split, publish-only events, append-only repos) for large ones. Both share the three function types (Pure Logic / Side Effect / Orchestration) and a Result/Option type for fallible returns. Storage-agnostic (works with any DB). Trigger on tasks like "add a use case", "write a repository", "wire a service", "review this domain module", or any question about where a function or file belongs.
license: MIT
---

# Go Func — Hexagonal Convention

A discipline for Go monoliths: business logic isolated from infrastructure, every function classified, state changes append-only where it pays off. Most decisions reduce to "which of the three function types is this, and where does it live."

## Choose a profile first

Pick the lightest profile the codebase can live with. **You can start Simple and graduate one domain at a time** — the function-type discipline is identical in both, so promoting a domain is moving files into folders, not a rewrite.

| | **Simple** | **Advanced** |
|---|---|---|
| Best for | small / early / few-domain monolith | large monolith, many domains, external integrations |
| Structure | one flat package per domain, **files not folders** | per-domain `domain/port/application/adapter` folders |
| Service → store | service may call the repository **directly** | publish-only events; subscribers persist |
| Repositories | normal CRUD is fine | append-only (no Update/Delete) |
| Interfaces (ports) | only where you need a seam (test/swap) | every service has a port + compile-time check |
| Fallible returns | plain `(T, error)` **or** `Result[T]` — pick one | `Result[T]` / `Option[T]` |

Rules below are tagged **[both]**, **[simple]**, or **[advanced]**. When unsure which profile a codebase uses: subfolders like `port/`/`adapter/` → Advanced; flat `*.go` files in a domain package → Simple.

## The one rule that drives everything — [both]

**Every function is exactly one of three types. Never mixed.** This holds in *both* profiles — Simple keeps the discipline even without the folders.

| Type | Does | Lives in (Advanced) | Returns |
|------|------|----------|---------|
| **Pure Logic** | No I/O, no side effects, deterministic | `domain/`, `application/`, `builders/`, `mappers/` | plain `T` (or `error` for validation) |
| **Side Effect** | DB, network, filesystem, `time.Now()`, random, queues | `adapter/` only | `Result[T]` / `Option[T]` (Advanced) or `(T, error)` (Simple) |
| **Orchestration** | Composes Pure + Side Effect, runs a use case | `application/` (Service methods) | `Result[T]` or `(T, error)` |

In **Simple**, the three types live as clearly-named functions/files in one package (e.g. `validate*` and `build*` are Pure, `*Repository` methods are Side Effect, `Service` methods are Orchestration) — same separation, no folder walls.

Corollaries — [both]:
- A function that calls `time.Now()` or `rand` is a Side Effect — keep it out of pure code.
- Pure validation that returns an `error` is still Pure Logic.
- Never embed business logic inside a Side Effect function. Keep adapters/repos thin.

## Folder layout

### Simple — [simple]

One package per domain, **flat files, no subfolders**. Add a folder only when a single file gets unwieldy.

```
internal/<domain>/
├── model.go        — structs, constants, value objects   (Pure Logic)
├── service.go      — use-case methods                     (Orchestration)
├── repository.go   — storage access                       (Side Effect)
├── handler.go      — HTTP transport                        (Side Effect)
└── port.go         — interface(s), only where you need a seam
```

Wire it in `cmd/<app>/main.go`. Cross-domain: still avoid importing another domain's internals — pass an injected interface. Promote to Advanced when a domain grows integrations, many aggregates, or needs the event/append-only guarantees.

### Advanced — [advanced]

Each domain follows this exactly:

```
internal/<domain>/
├── domain/        — models, constants, value objects, events/ (Pure Logic only)
├── port/          — interfaces only: UseCase (PortIn) + Repository (PortOut)
├── application/   — Service struct, builders/, mappers/, dto/ (Pure + Orchestration)
└── adapter/       — http/, repository/, subscribers/, workers/ (Side Effects)
```

Dependency direction points inward:

```
adapter → application → domain
                ↘ port ↙   (port = contracts; adapter implements them)
```

Domain depends on nothing. Port holds no implementations and no framework imports.

External systems get isolated bounded contexts under `internal/<domain>/integrations/<system>/`, each with its own domain/port/adapter/application and **no imports between integrations**.

## Ports

Naming and method shape apply in **[both]** profiles; mandatory ports/checks are **[advanced]**.

- **[both]** Name use-case methods after business actions: `PlaceOrder`, `CancelOrder`, `ViewOrderByID`. Never CRUD verbs (`Create`, `Get`, `Update`).
- **[simple]** Define an interface only where you need a seam — to mock in a test or swap an implementation. Don't wrap every service in a port reflexively.
- **[advanced]** Every service has a **PortIn (UseCase)** and depends on a **PortOut (Repository)**. `port/` is interfaces only — no structs, no ORM, no framework imports.
- **[advanced]** **PortOut (Repository)** is append-only: `Create*` and read methods only.
- **[advanced]** Compile-time check is mandatory for every service:

```go
var _ port.IOrderService = (*OrderServiceImpl)(nil)
```

- **[advanced]** Constructors return the port interface, not the concrete struct. HTTP handlers (`package httpadapter` in `adapter/http/`) depend on the UseCase interface, never on `*Service`.

## Append-only repositories — [advanced]

> **[simple]** profile: ordinary CRUD (including `Update`/`Delete`) is fine. Adopt the rules below only when you need an audit trail or version history.

**No `Update`. No `Delete`. Ever, on any repository interface.**

- An "update" → create a new version record referencing the original ID (`OriginalVersionID`, `VersionNo+1`).
- A "delete" → create a tombstone/cancellation record (id, reason, actor, timestamp). Original stays untouched.
- One repository owns one collection/table.

```go
// WRONG: repo.UpdateVersion(ctx, v)
newVersion := &models.OrderVersion{OriginalVersionID: v.ID, VersionNo: v.VersionNo + 1, /* amended */}
repo.CreateVersion(ctx, newVersion)
```

## Publish-only domain events — [advanced]

> **[simple]** profile: a service may call its repository directly (`repo.Create(...)`) and return. Reach for the event bus below only when more than one side effect must react to a change, or you want services ignorant of persistence.

A service does its work, fires one event, and returns a complete result. It never persists or calls another service. Side effects live in subscribers.

```
Service.DoThing()
  ├── validate                 ← Pure Logic
  ├── build entity (pre-gen ID) ← Pure Logic
  ├── publisher.Publish(event) ← the only boundary crossing
  └── return entity            ← caller gets the full result now

Bus dispatches → SaveHandler (repo.Create) │ NotificationHandler │ LogHandler
```

Rules:
- Service **never** calls `repo.Create` directly. Service **never** calls another Service directly.
- Pre-generate the ID before publishing so the caller receives a complete entity.
- Adding a side effect = a new subscriber + `bus.Subscribe`. The publishing service does not change.
- Subscribers (`adapter/subscribers/`) call repositories directly, handle one event type, and are ignorant of each other.

## Cross-domain communication — [both]

**Never import another domain's internals.** Sanctioned channels:
1. **Injected port interface** — define the interface in your domain, inject the implementation at wiring time. Best for synchronous queries. Works in both profiles.
2. **Domain events** — publish; the other module subscribes. Best for notifications, cascades, audit. **[advanced]**
3. **`pkg/`** — only for genuinely infrastructure-level utilities, not domain services.

## Result / Option conventions — [both]

- Pick **one** error convention per codebase and use it everywhere: plain `(T, error)` (fine for **[simple]**) or a `Result[T]` / `Option[T]` type (**[advanced]**, e.g. `github.com/samber/mo`). Don't mix both in one project.
- If using `Result`, wrap `func() (T, error)` into it at the adapter boundary so the `(T, error)` shape never leaks upward.
- Lazy singletons: defer expensive connections (DB, clients) to first use — wrap the constructor and guard it with `sync.Once` so it runs at most once.
- IDs: when a caller needs the entity back immediately, pre-generate the ID before the write, using whatever your store needs (e.g. `uuid.New()`, or a store-native ID). The ID type is an implementation detail.

## Testing shape

- **[simple]** **Service test** — call the service against an in-memory/fake repo (or a real test DB) and assert the result + that the repo was written.
- **[advanced]** **Service test** — assert the event was published. No repo mock needed.
- **[advanced]** **Subscriber test** — assert the repo method was called. No service involved.
- **[both]** **Repository test** — assert storage behavior (for append-only: original + amendment both exist; tombstone created, original intact).

## Review checklist

**[both]**
- [ ] Profile is clear and consistent across the codebase (don't half-migrate a domain)
- [ ] Every function is one clear type (Pure / Side Effect / Orchestration)
- [ ] No business logic inside repositories/adapters
- [ ] Use-case methods named by business action, not CRUD
- [ ] One error convention throughout (`(T, error)` *or* `Result[T]`, not both)
- [ ] No cross-domain imports of internals — use an injected interface

**[advanced] additionally**
- [ ] Folder layout matches `domain/port/application/adapter`
- [ ] Side Effect functions return `Result[T]`, not `(T, error)`
- [ ] No `Update`/`Delete` on any repository interface
- [ ] Services publish events; never call `repo.Create` or another service directly
- [ ] Pre-generated IDs before publishing
- [ ] `var _ port.UseCase = (*Impl)(nil)` present for every service
- [ ] HTTP handlers depend on port interfaces, not concrete structs
- [ ] No imports between integrations
- [ ] Workers accept `context.Context`, use `select` on `ctx.Done()`, bounded queues, balanced WaitGroups
