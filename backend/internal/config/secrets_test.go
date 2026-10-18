package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiredSecretsFailClosed(t *testing.T) {
	for _, key := range []string{"DATABASE_URL", "JWT_SECRET"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "")
			t.Setenv(key+"_FILE", "")
			if _, e := Require(key); e == nil {
				t.Fatal("missing config accepted")
			}
			t.Setenv(key, "synthetic-secret")
			t.Setenv(key+"_FILE", "/missing/synthetic")
			if _, e := Require(key); e == nil || strings.Contains(e.Error(), "synthetic-secret") {
				t.Fatal("silent fallback or secret in error")
			}
			p := filepath.Join(t.TempDir(), "secret")
			os.WriteFile(p, []byte("  synthetic-file-value "), 0600)
			t.Setenv(key+"_FILE", p)
			if v, e := Require(key); e != nil || v != "synthetic-file-value" {
				t.Fatal("file config failed")
			}
		})
	}
}
func TestOwnerOnlyMigrationConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_URL_FILE", "")
	t.Setenv("DATABASE_URL_OWNER_FILE", "")
	t.Setenv("DATABASE_URL_OWNER", "synthetic-owner")
	if v, e := DatabaseDSN(true); e != nil || v != "synthetic-owner" {
		t.Fatal("owner-only unsupported")
	}
	if _, e := DatabaseDSN(false); e == nil {
		t.Fatal("runtime selected owner")
	}
}
