# Advanced profile

Everything here adds to `SKILL.md`; the three function types and the shared rules still apply. Read it in full before changing an Advanced domain or graduating a Simple one. Code blocks start with their file path.

## Layout

```
internal/<domain>/
├── domain/        — models, constants, value objects, events/ (Pure Logic only)
├── port/          — interfaces only: UseCase (PortIn) + Repository (PortOut)
├── application/   — Service, builders/, mappers/, dto/ (Pure + Orchestration)
└── adapter/       — http/, repository/, subscribers/, workers/ (Side Effects)
```

Dependency direction points inward. `domain` imports nothing from the project, and `port` holds contracts with no implementations and no framework imports.

```
adapter → application → domain
                ↘ port ↙   (port = contracts; adapter implements them)
```

External systems get isolated bounded contexts under `internal/<domain>/integrations/<system>/`, each with its own `domain/port/adapter/application`, and integrations import nothing from each other.

## Ports

- Every service has a **PortIn (UseCase)** and depends on a **PortOut (Repository)**.
- `port/` is interfaces only.
- Constructors return the port interface. This overrides the general "return concrete types" idiom on purpose: callers can only reach the contract.
- HTTP handlers (`package httpadapter` in `adapter/http/`) depend on the UseCase interface.
- Every service has a compile-time check.

```go
// internal/order/port/port.go
package port

import (
	"context"

	"example.com/shop/internal/order/domain"
)

type OrderUseCase interface {
	PlaceOrder(ctx context.Context, in domain.PlaceOrderInput) (domain.Order, error)
	ViewOrderByID(ctx context.Context, originalID string) (domain.Order, error)
}

// OrderRepository is append-only: Create* and read methods.
type OrderRepository interface {
	CreateOrderVersion(ctx context.Context, o domain.Order) error
	LatestOrderVersion(ctx context.Context, originalID string) (domain.Order, error)
}

type Publisher interface {
	Publish(ctx context.Context, event any) error
}
```

## Append-only repositories

**A repository interface exposes `Create*` and reads.** One repository owns one collection or table.

- An "update" creates a new version. `OriginalVersionID` stays fixed across the whole chain, and `VersionNo` increments. A first version's `OriginalVersionID` is its own `ID`.
- A "delete" creates a tombstone or cancellation record (id, reason, actor, timestamp). The original stays untouched.

```go
// internal/order/domain/order.go
package domain

import (
	"errors"
	"time"
)

var (
	ErrEmptyOrder = errors.New("order: no items")
	ErrNotFound   = errors.New("order: not found")
	ErrConflict   = errors.New("order: version conflict")
)

type PlaceOrderInput struct {
	CustomerID string
	TotalCents int64
}

type Order struct {
	ID                string
	OriginalVersionID string
	VersionNo         int
	CustomerID        string
	TotalCents        int64
	CreatedAt         time.Time
}

func FirstVersion(in PlaceOrderInput, id string, now time.Time) Order {
	return Order{ID: id, OriginalVersionID: id, VersionNo: 1,
		CustomerID: in.CustomerID, TotalCents: in.TotalCents, CreatedAt: now}
}

func Amend(current Order, id string, totalCents int64, now time.Time) Order {
	next := current
	next.ID = id
	next.VersionNo = current.VersionNo + 1
	next.TotalCents = totalCents
	next.CreatedAt = now
	return next
}
```

Three consequences need handling in every append-only collection:

- **Reads:** the repository owns "current state". `Latest*` returns the highest `VersionNo` and returns `ErrNotFound` when the chain is tombstoned. A repository test covers both.
- **Concurrent amendments:** two writers can both build version 3. Put a unique constraint on `(OriginalVersionID, VersionNo)` and translate the violation to `ErrConflict` in the adapter. The client retries from the latest version.
- **Legal erasure:** retained history can conflict with deletion rights. Before putting personal data in an append-only collection, raise it with a human. The usual answers are a separate deletable store, or per-subject encryption with the key destroyed on erasure.

## Publish-only domain events

Writes leave a service only as an event. The service may read through its ports, then fires one event and returns a complete result. Side effects live in subscribers.

```
Service.DoThing()
  ├── validate                  ← Pure Logic
  ├── read through a port       ← only if the use case needs it
  ├── build entity (ID, now)    ← Pure Logic; values passed in
  ├── publisher.Publish(event)  ← the only write crossing the boundary
  └── return entity             ← caller gets the full result now

Bus dispatches → SaveHandler (repo.Create) │ NotificationHandler │ LogHandler
```

```go
// internal/order/domain/events/events.go
package events

import (
	"time"

	"example.com/shop/internal/order/domain"
)

// Every event carries EventID so subscribers can deduplicate.
type OrderPlaced struct {
	EventID    string
	OccurredAt time.Time
	Order      domain.Order
}
```

```go
// internal/order/application/service.go
package application

import (
	"context"
	"fmt"
	"time"

	"example.com/shop/internal/order/domain"
	"example.com/shop/internal/order/domain/events"
	"example.com/shop/internal/order/port"
)

var _ port.OrderUseCase = (*orderService)(nil)

type orderService struct {
	orders    port.OrderRepository
	publisher port.Publisher
	now       func() time.Time
	newID     func() string
}

func NewOrderService(orders port.OrderRepository, publisher port.Publisher,
	now func() time.Time, newID func() string) port.OrderUseCase {
	return &orderService{orders: orders, publisher: publisher, now: now, newID: newID}
}

func (s *orderService) PlaceOrder(ctx context.Context, in domain.PlaceOrderInput) (domain.Order, error) {
	if in.TotalCents <= 0 {
		return domain.Order{}, domain.ErrEmptyOrder
	}
	now := s.now()
	o := domain.FirstVersion(in, s.newID(), now)
	event := events.OrderPlaced{EventID: s.newID(), OccurredAt: now, Order: o}
	if err := s.publisher.Publish(ctx, event); err != nil {
		return domain.Order{}, fmt.Errorf("publish order placed: %w", err)
	}
	return o, nil
}

func (s *orderService) ViewOrderByID(ctx context.Context, originalID string) (domain.Order, error) {
	return s.orders.LatestOrderVersion(ctx, originalID)
}
```

Rules:

- A service writes only by publishing. It reads through ports, and reaches other domains through injected ports or events.
- Generate the ID before publishing so the caller receives a complete entity.
- Adding a side effect means a new subscriber and a `bus.Subscribe`. The publishing service stays unchanged.
- Subscribers (`adapter/subscribers/`) call repositories directly, handle one event type, and are ignorant of each other.
- Subscribers may import another domain's `domain/events` package. Those types are its published contract.

### Delivery guarantees

"Returns a complete result" is true only if the write succeeded. Pick the guarantee per subscriber:

| Subscriber | Dispatch | On failure |
|---|---|---|
| **State-changing save** | Synchronous, inside `Publish` | `Publish` returns the error and the service returns it to the caller |
| **Must-happen reaction** (billing, another domain's state) | Durable outbox, written in the save's transaction, then relayed | Retried until it succeeds, then parked for a human |
| **Best-effort reaction** (notification, log) | Asynchronous after the save | Logged; the caller's result stands |

- Asynchronous delivery is at-least-once, so asynchronous subscribers are **idempotent**, keyed on `EventID`.
- Asynchronous subscribers assume nothing about ordering across aggregates. When order matters, check `VersionNo` and ignore stale events.
- An asynchronous subscriber starts from `context.WithoutCancel(ctx)` so the end of the request does not cancel it.
- The save subscriber owns the transaction when one event writes several collections. It is the Advanced equivalent of the Simple service boundary, and external calls stay outside it.
- Without an outbox, a crash after the save loses the asynchronous reactions. State that limit when choosing an in-memory bus.

## Result and Option

With the `Result[T]` convention, wrap at the adapter boundary so `(T, error)` stays inside the adapter. Absence is `Option[T]` when "not there" is a normal outcome, and `ErrNotFound` when the caller asked for something specific.

```go
// internal/order/adapter/repository/result.go
package repository

import (
	"context"

	"example.com/shop/internal/order/domain"
	"github.com/samber/mo"
)

type OrderRepository struct {
	latest func(ctx context.Context, originalID string) (domain.Order, error)
}

func (r OrderRepository) LatestOrderVersion(ctx context.Context, originalID string) mo.Result[domain.Order] {
	return mo.TupleToResult(r.latest(ctx, originalID))
}
```

## Graduating a domain from Simple

Moving files is the mechanical part. Events and append-only change behaviour, so adopt each one only if that domain needs it, and finish one domain before starting the next.

1. **Layout:** move files into `domain/port/application/adapter`, fix imports, and add UseCase ports with their compile-time checks. Done when behaviour is unchanged and tests pass untouched.
2. **Events**, if more than one side effect reacts to a change: introduce the publisher, move `repo.Create` into a synchronous save subscriber, and classify the rest using the delivery table. Done when no service in the domain calls a repository write.
3. **Append-only**, if the domain needs history: add version columns, backfill existing rows as version 1 with `OriginalVersionID = ID`, then replace `Update`/`Delete`. This is a data migration, so load `safety-first` first. Done when archcheck reports no `append-only` violation.
4. **Error convention:** keep the codebase's existing one. Changing it is a separate codebase-wide decision.

## Advanced review checklist

Applies on top of the `SKILL.md` checklist. `archcheck` covers those marked *(auto)*.

- [ ] Folder layout matches `domain/port/application/adapter`
- [ ] Dependencies point inward *(auto)*
- [ ] No `Update`/`Delete` on any repository interface *(auto)*
- [ ] No imports between integrations *(auto)*
- [ ] Services write only by publishing events
- [ ] Every subscriber's delivery guarantee is chosen, and asynchronous ones are idempotent on `EventID`
- [ ] IDs generated before publishing
- [ ] `var _ port.XUseCase = (*impl)(nil)` present for every service
- [ ] HTTP handlers depend on port interfaces
- [ ] Append-only collections have the `(OriginalVersionID, VersionNo)` unique constraint
- [ ] Side Effect functions follow the codebase's one error convention
