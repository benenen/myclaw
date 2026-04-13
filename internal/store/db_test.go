package store

import (
	"testing"
)

func TestMigrateCreatesCoreTables(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "channel_accounts", "channel_bindings", "bots", "agent_capabilities"} {
		var count int64
		err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count).Error
		if err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if count == 0 {
			t.Fatalf("table %s not created", table)
		}
	}
}
