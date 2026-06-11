package seed

import (
	"context"
	"log"
	"os"

	"github.com/noelzappy/vaulx/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// AdminUser inserts a seed admin user when the users table is empty.
// Logs credentials to stdout only on first run; never on subsequent runs.
func AdminUser(ctx context.Context, queries db.Querier) {
	count, err := queries.CountUsers(ctx)
	if err != nil {
		log.Printf("seed: count users: %v", err)
		return
	}
	if count > 0 {
		return
	}

	email := os.Getenv("SEED_ADMIN_EMAIL")
	password := os.Getenv("SEED_ADMIN_PASSWORD")
	if email == "" || password == "" {
		log.Println("seed: SEED_ADMIN_EMAIL or SEED_ADMIN_PASSWORD not set — skipping")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("seed: bcrypt: %v", err)
	}

	if _, err = queries.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		Name:         "Admin",
		Role:         "admin",
		PasswordHash: string(hash),
	}); err != nil {
		log.Fatalf("seed: create admin: %v", err)
	}

	log.Printf("seed: admin created — email: %s", email)
}
