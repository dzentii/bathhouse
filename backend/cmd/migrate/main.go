package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	p, e := filepath.Abs(os.Getenv("MIGRATIONS_PATH"))
	if e != nil {
		log.Fatal("invalid migration path")
	}
	m, e := migrate.New("file://"+p, os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal("cannot initialize migrations")
	}
	defer func() {
		sourceErr, dbErr := m.Close()
		if sourceErr != nil || dbErr != nil {
			log.Print("migration connections did not close cleanly")
		}
	}()
	direction := "up"
	if len(os.Args) > 1 {
		direction = os.Args[1]
	}
	switch direction {
	case "up":
		e = m.Up()
	case "down":
		e = m.Steps(-1)
	default:
		log.Fatal("usage: migrate [up|down]")
	}
	if e != nil && !errors.Is(e, migrate.ErrNoChange) {
		log.Fatal("migration failed: ", e)
	}
	log.Print("migrations applied")
}
