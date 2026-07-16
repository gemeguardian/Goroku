package goroku

import (
	"context"
	"sync"
	"testing"
)

var testDatabaseInitMu sync.Mutex

func initializedTestDatabase(t *testing.T, db *Database) *Database {
	t.Helper()
	testDatabaseInitMu.Lock()
	originalBaseDir := BaseDir
	BaseDir = t.TempDir()
	err := db.Init("")
	BaseDir = originalBaseDir
	testDatabaseInitMu.Unlock()
	if err != nil {
		t.Fatalf("initialize test database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(context.Background()); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	return db
}
