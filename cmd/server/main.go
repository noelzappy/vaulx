package main

import (
	"log"

	"github.com/a-h/templ"
	"github.com/antonlindstrom/pgstore"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

// TODO: replace with actual server wiring in Task 14
func main() {
	// Suppress "imported and not used" by referencing package-level symbols.
	_ = templ.NopComponent
	_ = pgstore.PGStore{}
	_ = aws.Config{}
	_ = config.WithRegion
	_ = credentials.NewStaticCredentialsProvider
	_ = s3.New
	_ = chi.NewRouter
	_ = migrate.ErrNoChange
	_ = securecookie.New
	_ = sessions.NewCookieStore
	_ = pgx.ErrNoRows
	_ = godotenv.Load
	_ = bcrypt.DefaultCost

	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found")
	}

	log.Println("vaulx scaffold ready")
}
