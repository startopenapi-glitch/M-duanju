package main

// 短剧平台 —— 单二进制双角色
//
//	drama-server api             HTTP 服务（无状态，可水平扩容）
//	drama-server worker          任务执行器（扫表认领，重启不丢任务）
//	drama-server migrate         执行数据库迁移
//	drama-server grant-runtime   创建受限运行角色
//
// 拆分理由：生成任务单段耗时 5-7 分钟，跑在 API 进程里会阻塞请求，
// 且服务重启即丢失。分离后 API 保持无状态，worker 可独立重启扩容。

import (
	"database/sql"
	"log"
	"os"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"

	"drama-platform/internal/config"
	"drama-platform/internal/handler"
	"drama-platform/internal/role"
	"drama-platform/internal/router"
	"drama-platform/internal/service"
	"drama-platform/internal/worker"
)

func main() {
	cmd := "api"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	dsn, err := config.DatabaseDSN(cmd == "migrate" || cmd == "grant-runtime")
	if err != nil {
		log.Fatal(err)
	}

	switch cmd {
	case "migrate":
		// 迁移必须用 owner 角色（需要 DDL 权限）；运行用受限角色
		if err := service.RunMigrations(dsn, "migrations"); err != nil {
			log.Fatal(err)
		}
		log.Println("迁移完成")
		return

	case "grant-runtime":
		if err := role.GrantRuntime(dsn); err != nil {
			log.Fatal(err)
		}
		return

	case "worker":
		runWorker(dsn)

	case "api":
		runAPI(dsn)

	default:
		log.Fatalf("未知命令 %q，可用：api | worker | migrate | grant-runtime", cmd)
	}
}

func openDB(dsn string) *sql.DB {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal("数据库连接失败，请检查 DATABASE_URL")
	}
	if err := db.Ping(); err != nil {
		log.Fatal("数据库不可达，请检查 DATABASE_URL")
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	return db
}

func runAPI(dsn string) {
	jwtSecret, err := config.Require("JWT_SECRET")
	if err != nil {
		log.Fatal(err)
	}
	db := openDB(dsn)
	defer db.Close()

	svc := service.New(db, jwtSecret)
	h := handler.New(svc, jwtSecret)

	// API 不再执行生成任务；任务恢复由 worker 的 ReclaimStaleJobs 接管
	gin.SetMode(gin.ReleaseMode)
	r := router.Setup(h, jwtSecret, os.Getenv("STATIC_DIR"))

	port := getenv("PORT", "8080")
	bind := getenv("BIND_ADDR", "127.0.0.1")
	log.Printf("[api] 监听 %s:%s（生成任务由 worker 进程执行）", bind, port)
	if err := r.Run(bind + ":" + port); err != nil {
		log.Fatal(err)
	}
}

func runWorker(dsn string) {
	jwtSecret, err := config.Require("JWT_SECRET")
	if err != nil {
		log.Fatal(err)
	}
	db := openDB(dsn)
	defer db.Close()

	svc := service.New(db, jwtSecret)

	// 支付对账也归 worker（与 API 解耦）
	svc.StartReconcileWorker()

	worker.New(svc).Run()
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
