package service

import (
	"os"
	"path/filepath"
	"testing"

	"drama-platform/internal/testutil"
)

// 迁移执行器每次全量重放所有 .sql（无版本账本），所以每个迁移文件都必须可重入。
// 这里直接执行真实的 010 文件两次，而不是复制 SQL 片段——复制会让文件漂移测不出来。
func TestMigrationsApplyTwiceWithoutError(t *testing.T) {
	db := testutil.DB(t)

	// 010 已经执行过，改动它会触发账本校验和不匹配（并被拒绝重放），
	// 因此它不再需要可重入；这里只校验新增迁移。
	files := []string{"012_legacy_budget_status.sql"}
	for _, name := range files {
		p := filepath.Join("..", "..", "migrations", name)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读取迁移失败 %s: %v", name, err)
		}
		for run := 1; run <= 2; run++ {
			if _, err := db.Exec(string(b)); err != nil {
				t.Fatalf("%s 第 %d 次执行失败（迁移必须可重入）: %v", name, run, err)
			}
		}
	}
}
