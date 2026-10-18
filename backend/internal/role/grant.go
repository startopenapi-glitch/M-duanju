package role

// 数据库角色分离
//
// 原则：迁移用所有者（owner）角色，运行用受限角色（runtime）。
// 受限角色只有 DML 权限（SELECT/INSERT/UPDATE/DELETE），没有 DDL。
// 即使发生 SQL 注入，攻击者也无法 DROP TABLE / ALTER TABLE。
//
// 用法：
//   DATABASE_URL=... drama-server migrate        # owner 执行
//   DATABASE_URL=... drama-server grant-runtime  # 创建/授权 runtime 角色
//   应用用 DATABASE_URL 指向 runtime 角色连接

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const runtimeRoleName = "duanju_runtime"

// GrantRuntime 创建受限运行角色并授予 DML 权限
func GrantRuntime(ownerDSN string) error {
	db, err := sql.Open("pgx", ownerDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("数据库不可达: %w", err)
	}

	// 密码从环境变量或文件读，不写死
	pwd := os.Getenv("APP_DB_PASSWORD")
	if p, ok := os.LookupEnv("APP_DB_PASSWORD_FILE"); ok && p != "" {
		if b, err := os.ReadFile(p); err == nil {
			pwd = string(b)
		}
	}
	if pwd == "" {
		return fmt.Errorf("请设置 APP_DB_PASSWORD 或 APP_DB_PASSWORD_FILE")
	}

	// 1. 创建角色（已存在则改密码）
	var exists bool
	db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, runtimeRoleName).Scan(&exists)
	if exists {
		if _, err := db.Exec(fmt.Sprintf(`ALTER ROLE %s WITH LOGIN PASSWORD '%s'`,
			runtimeRoleName, escape(pwd))); err != nil {
			return fmt.Errorf("更新角色密码失败: %w", err)
		}
		log.Printf("角色 %s 已存在，密码已更新", runtimeRoleName)
	} else {
		if _, err := db.Exec(fmt.Sprintf(`CREATE ROLE %s WITH LOGIN PASSWORD '%s'`,
			runtimeRoleName, escape(pwd))); err != nil {
			return fmt.Errorf("创建角色失败: %w", err)
		}
		log.Printf("已创建受限角色 %s", runtimeRoleName)
	}

	// 2. 基础权限：连库、用 schema
	stmts := []string{
		fmt.Sprintf(`GRANT CONNECT ON DATABASE %s TO %s`, currentDB(db), runtimeRoleName),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s`, runtimeRoleName),
		// 3. 现有表的 DML
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s`, runtimeRoleName),
		// 4. 现有序列（app_gen_id 用 md5 不依赖序列，但保险）
		fmt.Sprintf(`GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %s`, runtimeRoleName),
		// 5. 未来新建的表也自动授予（关键：否则每次迁移后都要重跑）
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s`, runtimeRoleName),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO %s`, runtimeRoleName),
		// 6. 明确收回 DDL 能力（防默认权限继承）
		fmt.Sprintf(`REVOKE CREATE ON SCHEMA public FROM %s`, runtimeRoleName),
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("授权失败 [%s]: %w", s, err)
		}
	}

	log.Printf("✓ %s 已获得 DML 权限（无 DDL）", runtimeRoleName)
	log.Printf("  使用方式：DATABASE_URL=postgres://%s:<密码>@127.0.0.1:5432/duanju", runtimeRoleName)
	return nil
}

func currentDB(db *sql.DB) string {
	var name string
	db.QueryRow(`SELECT current_database()`).Scan(&name)
	return name
}

// escape 转义单引号，防 SQL 注入（密码来自环境变量）
func escape(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\'' {
			out = append(out, '\'', '\'')
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}
