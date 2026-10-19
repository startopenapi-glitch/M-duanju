package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// RunMigrations executes each immutable file and its ledger entry in one
// transaction. One session lock spans the full upgrade, including preflight.
func RunMigrations(dsn, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		return fmt.Errorf("数据库配置无效")
	}
	defer db.Close()
	if e = db.PingContext(ctx); e != nil {
		return fmt.Errorf("迁移数据库连接失败，请检查 DATABASE_URL_OWNER")
	}
	return runMigrationsDB(ctx, db, dir)
}
func runMigrationsDB(ctx context.Context, db *sql.DB, dir string) error {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return fmt.Errorf("迁移目录不可读")
	}
	type file struct {
		name, checksum string
		body           []byte
	}
	files := []file{}
	versions := map[string]bool{}
	byName := map[string]string{}
	pattern := regexp.MustCompile(`^([0-9]{3})_[a-z0-9_]+\.sql$`)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".sql" {
			continue
		}
		match := pattern.FindStringSubmatch(name)
		if match == nil || versions[match[1]] {
			return fmt.Errorf("迁移文件名或版本冲突: %s", name)
		}
		versions[match[1]] = true
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("迁移不可读: %s", name)
		}
		sum := sha256.Sum256(b)
		checksum := hex.EncodeToString(sum[:])
		files = append(files, file{name, checksum, b})
		byName[name] = checksum
	}
	if len(files) == 0 {
		return fmt.Errorf("迁移目录没有版本文件")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	conn, e := db.Conn(ctx)
	if e != nil {
		return fmt.Errorf("无法获取迁移连接")
	}
	defer conn.Close()
	// Scope by schema as well as database so isolated schemas never block each other.
	if _, e = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext(current_database()||'.'||current_schema()||':drama-migrations'))`); e != nil {
		return fmt.Errorf("获取迁移锁失败")
	}
	defer func() {
		unlock, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn.ExecContext(unlock, `SELECT pg_advisory_unlock(hashtext(current_database()||'.'||current_schema()||':drama-migrations'))`)
	}()
	if _, e = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(name TEXT PRIMARY KEY,checksum CHAR(64) NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); e != nil {
		return fmt.Errorf("迁移账本不可用")
	}
	rows, e := conn.QueryContext(ctx, `SELECT name,checksum FROM schema_migrations ORDER BY name`)
	if e != nil {
		return fmt.Errorf("读取迁移账本失败")
	}
	applied := map[string]string{}
	last := ""
	for rows.Next() {
		var name, sum string
		if e = rows.Scan(&name, &sum); e != nil {
			rows.Close()
			return fmt.Errorf("迁移账本格式无效")
		}
		applied[name] = sum
		last = name
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return fmt.Errorf("读取迁移账本失败")
	}
	// Check *all* known files before executing any new statement. Older binaries
	// cannot silently run against a newer or edited migration history.
	for name, sum := range applied {
		if byName[name] != sum {
			return fmt.Errorf("迁移校验和变化或文件缺失: %s", name)
		}
	}
	for _, f := range files {
		if _, ok := applied[f.name]; ok {
			continue
		}
		if f.name < last {
			return fmt.Errorf("拒绝补入历史版本: %s", f.name)
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("启动迁移事务失败: %s", f.name)
		}
		if _, err = tx.ExecContext(ctx, string(f.body)); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, f.name, f.checksum)
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("迁移 %s 失败，已回滚本文件；请核对隔离预检与数据库状态", f.name)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("迁移 %s 提交未确认，请检查账本后重试", f.name)
		}
	}
	return nil
}
