package model

import (
	"slices"
	"testing"
)

func TestPermissions(t *testing.T) {
	c := Connection{AllowInsert: true, AllowDDL: true}
	if !c.Allows(OpSelect) || !c.Allows(OpInsert) || c.Allows(OpDelete) || c.Allows("other") {
		t.Fatal("Allows")
	}
	if got := c.AllowedOps(); !slices.Equal(got, []Op{OpSelect, OpInsert, OpDDL}) {
		t.Fatalf("AllowedOps = %v", got)
	}
	if got := c.Missing([]Op{OpInsert, OpUpdate, OpDelete, OpUpdate}); !slices.Equal(got, []Op{OpUpdate, OpDelete}) {
		t.Fatalf("Missing = %v", got)
	}
}

func TestDBTypes(t *testing.T) {
	if !Postgres.Valid() || DBType("oracle").Valid() {
		t.Fatal("Valid")
	}
	if !SQLite.IsSQL() || MongoDB.IsSQL() || MongoDB.Info().Label != "MongoDB" {
		t.Fatal("IsSQL / Info")
	}
}
