package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/antonlindstrom/pgstore"
	"github.com/noelzappy/vaulx/internal/auth"
	"github.com/noelzappy/vaulx/internal/db"
	"github.com/noelzappy/vaulx/internal/handler"
	"github.com/noelzappy/vaulx/internal/seed"
	"github.com/noelzappy/vaulx/internal/storage"
	"github.com/noelzappy/vaulx/migrations"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	gorillasessions "github.com/gorilla/sessions"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()

	runMigrations()

	db.Connect(ctx)

	if err := storage.Connect(ctx); err != nil {
		log.Fatalf("fatal: cannot connect to S3 storage: %v", err)
	}
	if err := storage.EnsureCORS(ctx); err != nil {
		log.Printf("warning: could not set bucket CORS policy: %v", err)
	} else {
		log.Println("storage: CORS policy applied")
	}

	queries := db.New(db.DB)
	seed.AdminUser(ctx, queries)

	sessionStore, err := pgstore.NewPGStore(os.Getenv("DATABASE_URL"), []byte(os.Getenv("SESSION_SECRET")))
	if err != nil {
		log.Fatalf("session store: %v", err)
	}
	defer sessionStore.StopCleanup(sessionStore.Cleanup(5 * time.Minute))
	sessionStore.Options = &gorillasessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	}

	authHandler       := handler.NewAuthHandler(queries, sessionStore)
	filesHandler      := handler.NewFilesHandler(queries)
	uploadHandler     := handler.NewUploadHandler(queries)
	downloadHandler   := handler.NewDownloadHandler(queries)
	adminHandler      := handler.NewAdminHandler(queries)
	shareHandler      := handler.NewShareHandler(queries)
	permissionHandler := handler.NewPermissionHandler(queries)
	multipartHandler  := handler.NewMultipartHandler(queries)

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	r.Route("/auth", func(r chi.Router) {
		r.Get("/login", authHandler.LoginPage)
		r.With(httprate.LimitByIP(10, 1*time.Minute)).Post("/login", authHandler.LoginPage)
		r.Post("/logout", authHandler.Logout)
	})

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/files", http.StatusFound)
	})

	// Public share routes — no auth required
	r.Get("/s/{slug}", shareHandler.ResolveShare)
	r.Get("/s/{slug}/file/{fileID}", shareHandler.SharedFileDownload)

	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth(sessionStore))
		r.Get("/files", filesHandler.List)
		r.Get("/upload", filesHandler.UploadPage)
		r.Post("/files/folders", filesHandler.CreateFolder)
		r.Delete("/files/folders/{folderID}", filesHandler.DeleteFolder)
		r.Patch("/files/folders/{folderID}", filesHandler.RenameFolder)
		r.Post("/files/folders/{folderID}/share", shareHandler.CreateFolderShare)

		// Search + download — registered BEFORE /files/{folderID} to avoid wildcard swallowing
		r.Get("/files/search", filesHandler.Search)
		r.Get("/files/{fileID}/download", downloadHandler.Download)

		// Folder listing wildcard — must come after the download route
		r.Get("/files/{folderID}", filesHandler.ListFolder)

		// File operations
		r.Delete("/files/{fileID}", filesHandler.DeleteFile)
		r.Patch("/files/{fileID}/name", filesHandler.RenameFile)
		r.Patch("/files/{fileID}/folder", filesHandler.MoveFile)
		r.Post("/files/{fileID}/share", shareHandler.CreateShare)

		// Share management
		r.Get("/shares", shareHandler.SharesPage)
		r.Delete("/shares/{shareID}", shareHandler.RevokeShare)

		// Upload
		r.Post("/api/upload/init", uploadHandler.InitUpload)
		r.Post("/api/upload/confirm/{fileID}", uploadHandler.ConfirmUpload)

		// Preview
		r.Get("/api/file/{fileID}/preview", filesHandler.PreviewFile)
		r.Get("/api/folders", filesHandler.ListFoldersJSON)

		// Multipart upload
		r.Route("/api/s3/multipart", func(r chi.Router) {
			r.Post("/", multipartHandler.CreateMultipartUpload)
			r.Get("/{uploadId}", multipartHandler.ListParts)
			r.Get("/{uploadId}/{partNumber}", multipartHandler.PresignPart)
			r.Delete("/{uploadId}", multipartHandler.AbortMultipartUpload)
			r.Post("/{uploadId}/complete", multipartHandler.CompleteMultipartUpload)
		})

		// Admin
		r.Route("/admin", func(r chi.Router) {
			r.Get("/users", adminHandler.ListUsers)
			r.Post("/users", adminHandler.CreateUser)
			r.Patch("/users/{userID}", adminHandler.UpdateUser)
			r.Get("/audit", adminHandler.AuditLog)
			r.Get("/trash", adminHandler.Trash)
			r.Post("/trash/{fileID}/restore", adminHandler.RestoreFile)
			r.Get("/permissions", permissionHandler.List)
			r.Post("/permissions", permissionHandler.Grant)
			r.Delete("/permissions/{permID}", permissionHandler.Revoke)
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("server: listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func runMigrations() {
	databaseURL := os.Getenv("DATABASE_URL")

	sqlDB, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("migrations: open db: %v", err)
	}
	defer sqlDB.Close()

	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		log.Fatalf("migrations: driver: %v", err)
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		log.Fatalf("migrations: source: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		log.Fatalf("migrations: init: %v", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("migrations: up: %v", err)
	}
	log.Println("migrations: applied")
}
