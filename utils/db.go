package utils

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	"github.com/tursodatabase/go-libsql"
	_ "github.com/tursodatabase/go-libsql"

	"polynux/disgoroq/db"
)

var DB *sql.DB
var Q *db.Queries

func Connect() *sql.DB {
	dbName := "local.db"
	dbUrl := GetEnv("DB_URL")
	dbToken := GetEnv("DB_TOKEN")
	if dbUrl == "" {
		log.Fatal("DB_URL is not set")
		os.Exit(1)
	}
	if dbToken == "" {
		log.Fatal("DB_TOKEN is not set")
		os.Exit(1)
	}
	dir, err := os.MkdirTemp("", "libsql-*")
	if err != nil {
		log.Fatalf("Error creating temp directory: %v", err)
		os.Exit(1)
	}

	dbPath := filepath.Join(dir, dbName)

	connector, err := libsql.NewEmbeddedReplicaConnector(dbPath, dbUrl, libsql.WithAuthToken(dbToken), libsql.WithSyncInterval(time.Minute))
	if err != nil {
		log.Fatalf("Error creating connector: %v", err)
		os.Exit(1)
	}

	db := sql.OpenDB(connector)

	return db
}

func InitializeDB() {
	LoadEnv()
	DB = Connect()
	Q = db.New(DB)
	CreateTables(context.Background())
}

func LoadSql() string {
	sql, err := os.ReadFile("schema.sql")
	if err != nil {
		log.Fatalf("Error reading schema.sql: %v", err)
		os.Exit(1)
	}
	return string(sql)
}

func CreateTables(ctx context.Context) {
	schema := LoadSql()

	_, err := DB.ExecContext(ctx, schema)
	if err != nil {
		log.Fatalf("Error creating tables: %v", err)
		os.Exit(1)
	}
}

func LoadEnv() {
	err := godotenv.Load(".env.local")
	if err != nil {
		log.Fatalf("Error loading .env file: %v", err)
		os.Exit(1)
	}
}

func GetEnv(key string) string {
	return os.Getenv(key)
}
