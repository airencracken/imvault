package store

import (
	"database/sql"
	"github.com/airencracken/comfylib/displaytime/displaytimetest"
	"path/filepath"
	"testing"
)

func TestSharedDisplayTimezoneSQLiteContract(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "display-time.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	displaytimetest.Run(t, db)
}
