# Worked example: PlaceOrder (Simple profile)

One use case end to end. Copy the shape, not the names. The first comment of each block is the file's path. For the Advanced differences, see [`advanced.md`](advanced.md).

The use case reads from another domain, then writes to two collections atomically. It shows the rules that are easiest to get wrong: `now` and `id` passed into Pure code, the external call outside the transaction, storage errors translated at the adapter, and HTTP status decided in one place.

## Pure Logic

No `context`, no clock, no ID generator. Everything arrives as a parameter, so the test needs no fakes.

```go
// internal/order/model.go
package order

import (
	"errors"
	"time"
)

var (
	ErrEmptyOrder      = errors.New("order: no items")
	ErrInvalidQuantity = errors.New("order: quantity must be positive")
	ErrOutOfStock      = errors.New("order: item out of stock")
	ErrNotFound        = errors.New("order: not found")
)

type Item struct {
	SKU       string
	Quantity  int
	UnitCents int64
}

type Order struct {
	ID         string
	CustomerID string
	Items      []Item
	TotalCents int64
	PlacedAt   time.Time
}

type PlaceOrderInput struct {
	CustomerID string
	Items      []Item
}

func validatePlaceOrder(in PlaceOrderInput) error {
	if len(in.Items) == 0 {
		return ErrEmptyOrder
	}
	for _, item := range in.Items {
		if item.Quantity <= 0 {
			return ErrInvalidQuantity
		}
	}
	return nil
}

func buildOrder(in PlaceOrderInput, id string, now time.Time) Order {
	var total int64
	for _, item := range in.Items {
		total += int64(item.Quantity) * item.UnitCents
	}
	return Order{ID: id, CustomerID: in.CustomerID, Items: in.Items, TotalCents: total, PlacedAt: now}
}
```

## Seams

`StockChecker` is defined here, by the domain that consumes it. The `inventory` domain never appears in this package.

```go
// internal/order/port.go
package order

import "context"

type Repository interface {
	Create(ctx context.Context, o Order) error
	FindByID(ctx context.Context, id string) (Order, error)
}

type AuditLog interface {
	Record(ctx context.Context, orderID, action string) error
}

type StockChecker interface {
	Available(ctx context.Context, items []Item) (bool, error)
}

// Transactor runs fn atomically. Repositories join the transaction through ctx.
type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
```

## Orchestration

The service holds the sequence and no business rules. `now` and `newID` are its Side Effect inputs. The stock check stays outside the transaction.

```go
// internal/order/service.go
package order

import (
	"context"
	"fmt"
	"time"
)

type Service struct {
	orders Repository
	audit  AuditLog
	stock  StockChecker
	tx     Transactor
	now    func() time.Time
	newID  func() string
}

func NewService(orders Repository, audit AuditLog, stock StockChecker, tx Transactor,
	now func() time.Time, newID func() string) *Service {
	return &Service{orders: orders, audit: audit, stock: stock, tx: tx, now: now, newID: newID}
}

func (s *Service) PlaceOrder(ctx context.Context, in PlaceOrderInput) (Order, error) {
	if err := validatePlaceOrder(in); err != nil {
		return Order{}, err
	}
	available, err := s.stock.Available(ctx, in.Items)
	if err != nil {
		return Order{}, fmt.Errorf("check stock: %w", err)
	}
	if !available {
		return Order{}, ErrOutOfStock
	}

	o := buildOrder(in, s.newID(), s.now())
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.orders.Create(ctx, o); err != nil {
			return err
		}
		return s.audit.Record(ctx, o.ID, "placed")
	})
	if err != nil {
		return Order{}, fmt.Errorf("place order: %w", err)
	}
	return o, nil
}
```

## Side Effects

The transaction lives in a shared package because every domain's repositories need the same context key. Translate storage errors here; `sql.ErrNoRows` goes no further than this file.

```go
// internal/platform/dbtx/dbtx.go
package dbtx

import (
	"context"
	"database/sql"
	"fmt"
)

type txKey struct{}

// Querier is the part of *sql.DB and *sql.Tx that repositories use.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// From returns the transaction in ctx, or db when there is none.
func From(ctx context.Context, db *sql.DB) Querier {
	if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return tx
	}
	return db
}

type Transactor struct{ DB *sql.DB }

func (t Transactor) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() // no-op after Commit

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
```

```go
// internal/order/repository.go
package order

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"example.com/shop/internal/platform/dbtx"
)

type SQLRepository struct{ DB *sql.DB }

func (r SQLRepository) Create(ctx context.Context, o Order) error {
	_, err := dbtx.From(ctx, r.DB).ExecContext(ctx,
		`INSERT INTO orders (id, customer_id, total_cents, placed_at) VALUES ($1, $2, $3, $4)`,
		o.ID, o.CustomerID, o.TotalCents, o.PlacedAt)
	if err != nil {
		return fmt.Errorf("insert order %s: %w", o.ID, err)
	}
	return nil
}

func (r SQLRepository) FindByID(ctx context.Context, id string) (Order, error) {
	var o Order
	err := dbtx.From(ctx, r.DB).QueryRowContext(ctx,
		`SELECT id, customer_id, total_cents, placed_at FROM orders WHERE id = $1`, id).
		Scan(&o.ID, &o.CustomerID, &o.TotalCents, &o.PlacedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("select order %s: %w", id, err)
	}
	return o, nil
}
```

The handler decodes, calls one use case, and encodes. `statusFor` is the only place domain errors become HTTP.

```go
// internal/order/handler.go
package order

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

type Handler struct{ Service *Service }

func (h Handler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	var in PlaceOrderInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}
	o, err := h.Service.PlaceOrder(r.Context(), in)
	if err != nil {
		status := statusFor(err)
		if status == http.StatusInternalServerError {
			slog.ErrorContext(r.Context(), "place order failed", "err", err)
			http.Error(w, "internal error", status)
			return
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(o); err != nil {
		slog.ErrorContext(r.Context(), "encode response failed", "err", err)
	}
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrEmptyOrder), errors.Is(err, ErrInvalidQuantity):
		return http.StatusUnprocessableEntity
	case errors.Is(err, ErrOutOfStock):
		return http.StatusConflict
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}
```

## Tests

A table for Pure Logic, and fakes with a fixed clock for Orchestration.

```go
// internal/order/service_test.go
package order

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidatePlaceOrder(t *testing.T) {
	cases := []struct {
		name string
		in   PlaceOrderInput
		want error
	}{
		{"valid", PlaceOrderInput{Items: []Item{{SKU: "a", Quantity: 1}}}, nil},
		{"no items", PlaceOrderInput{}, ErrEmptyOrder},
		{"zero quantity", PlaceOrderInput{Items: []Item{{SKU: "a"}}}, ErrInvalidQuantity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validatePlaceOrder(c.in); !errors.Is(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

type fakeRepo struct {
	created []Order
	err     error
}

func (f *fakeRepo) Create(_ context.Context, o Order) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, o)
	return nil
}

func (f *fakeRepo) FindByID(context.Context, string) (Order, error) {
	return Order{}, ErrNotFound
}

type fakeAudit struct{ actions []string }

func (f *fakeAudit) Record(_ context.Context, _, action string) error {
	f.actions = append(f.actions, action)
	return nil
}

type stockFunc func() (bool, error)

func (f stockFunc) Available(context.Context, []Item) (bool, error) { return f() }

type inlineTx struct{}

func (inlineTx) WithinTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func newTestService(repo *fakeRepo, audit *fakeAudit, stock stockFunc) *Service {
	now := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	return NewService(repo, audit, stock, inlineTx{}, now, func() string { return "id-1" })
}

func TestPlaceOrder(t *testing.T) {
	in := PlaceOrderInput{CustomerID: "c1", Items: []Item{{SKU: "a", Quantity: 2, UnitCents: 150}}}

	t.Run("persists the order and audits it", func(t *testing.T) {
		repo, audit := &fakeRepo{}, &fakeAudit{}
		svc := newTestService(repo, audit, func() (bool, error) { return true, nil })

		o, err := svc.PlaceOrder(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if o.ID != "id-1" || o.TotalCents != 300 {
			t.Fatalf("unexpected order: %+v", o)
		}
		if len(repo.created) != 1 || len(audit.actions) != 1 {
			t.Fatalf("created=%d audited=%d", len(repo.created), len(audit.actions))
		}
	})

	t.Run("out of stock writes nothing", func(t *testing.T) {
		repo, audit := &fakeRepo{}, &fakeAudit{}
		svc := newTestService(repo, audit, func() (bool, error) { return false, nil })

		if _, err := svc.PlaceOrder(context.Background(), in); !errors.Is(err, ErrOutOfStock) {
			t.Fatalf("got %v", err)
		}
		if len(repo.created) != 0 {
			t.Fatal("wrote an order despite no stock")
		}
	})

	t.Run("repository failure skips the audit and keeps the cause", func(t *testing.T) {
		boom := errors.New("boom")
		repo, audit := &fakeRepo{err: boom}, &fakeAudit{}
		svc := newTestService(repo, audit, func() (bool, error) { return true, nil })

		if _, err := svc.PlaceOrder(context.Background(), in); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
		if len(audit.actions) != 0 {
			t.Fatal("audited an order that was not stored")
		}
	})
}
```
