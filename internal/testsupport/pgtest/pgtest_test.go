//go:build !unit && !e2e

package pgtest

import "testing"

func TestFreshDatabaseIsMigratedAndIsolated(t *testing.T) {
	url := FreshDatabase(t)
	db := Open(t, url)
	var count int64
	if err := db.Raw(`SELECT count(*) FROM outbox_events`).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("fresh outbox count = %d err %v", count, err)
	}
	if err := db.Exec(`INSERT INTO inbox_messages VALUES ('c', 'm', repeat('a', 64), now(), now())`).Error; err != nil {
		t.Fatalf("wallet_app must write the fresh schema: %v", err)
	}
}
