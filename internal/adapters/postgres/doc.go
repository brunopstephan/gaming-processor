// Package postgres is the persistence adapter: GORM with explicit locking,
// conflict and version clauses, a context-bound transaction manager and
// mappers between table rows and domain aggregates. Invariants also live in
// the schema (migrations/), so they hold even if this code misbehaves.
package postgres
