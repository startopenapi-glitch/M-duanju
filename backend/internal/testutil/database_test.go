package testutil

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDatabaseGuardRejectsOtherDatabaseBeforeConnecting(t *testing.T) {
	if os.Getenv("AUTH_GUARD_CHILD") == "1" {
		DB(t)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDatabaseGuardRejectsOtherDatabaseBeforeConnecting$")
	cmd.Env = append(os.Environ(), "AUTH_GUARD_CHILD=1", "TEST_DATABASE_URL=postgres://synthetic:unused@127.0.0.1:1/production")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "refusing database other than drama_remediation_test_20261017") {
		t.Fatalf("guard did not reject before connection: %v %s", err, output)
	}
}
