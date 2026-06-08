package db

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

var DB *pgxpool.Pool

func Connect(ctx context.Context) {
	var err error
	DB, err = pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db: create pool: %v", err)
	}
	if err := DB.Ping(ctx); err != nil {
		log.Fatalf("db: ping: %v", err)
	}
	log.Println("db: connected")
}
