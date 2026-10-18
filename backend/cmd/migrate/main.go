package main

import (
	"drama-platform/internal/config"
	"drama-platform/internal/service"
	_ "github.com/jackc/pgx/v5/stdlib"
	"log"
)

func main() {
	dsn, e := config.DatabaseDSN(true)
	if e != nil {
		log.Fatal(e)
	}
	if e = service.RunMigrations(dsn, "migrations"); e != nil {
		log.Fatal(e)
	}
	log.Print("迁移完成")
}
