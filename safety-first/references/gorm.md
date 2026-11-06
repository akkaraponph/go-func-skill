# GORM safety rules

These apply `safety-first` to GORM. The transaction and soft-delete rules in `SKILL.md` still hold.

## Migrations

`db.AutoMigrate()` is for local development and prototyping. Production uses versioned schema migrations, such as golang-migrate.

## Partial updates

`Save()` writes every field, including zero values, so it can silently overwrite data. Use `Updates(map[string]any{...})` for partial updates.

## Hooks

Keep `BeforeCreate` to mechanical field setup, such as generating an ID. Business logic belongs in the service layer.

## Connections

- Inject `*gorm.DB` through constructors, so business logic has no global `DB` to reach for.
- Use `r.db.WithContext(ctx)` wherever a context is available, so cancellation and timeouts reach the query.

## Transactions

`SkipDefaultTransaction: true` stops GORM wrapping single writes in an implicit transaction. Check whether the project sets it:

- Multi-table writes need an explicit `db.Transaction(...)` either way.
- A single-table write relies on the database's autocommit.

## Soft delete

- `gorm.DeletedAt` makes `db.Delete()` set `deleted_at`, and GORM adds `WHERE deleted_at IS NULL` to queries.
- `db.Unscoped()` includes soft-deleted rows. On a model with `gorm.DeletedAt`, `db.Unscoped().Delete()` is a hard delete, so it needs the human approval in `SKILL.md`.
- An `is_active` flag gets no automatic filter, so every query must add it.
