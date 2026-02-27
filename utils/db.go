package utils

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tursodatabase/go-libsql"
	_ "github.com/tursodatabase/go-libsql"

	"polynux/disgoroq/config"
	"polynux/disgoroq/db"
)

var DB *sql.DB
var Q *db.Queries

func GetDB() *sql.DB {
	return DB
}

// Connect connects to a remote Turso database using the provided configuration.
func Connect(cfg *config.DatabaseConfig) *sql.DB {
	dbName := "local.db"
	dbUrl := cfg.URL
	dbToken := cfg.Token

	if dbUrl == "" {
		log.Fatal("Database URL is not set")
		os.Exit(1)
	}
	if dbToken == "" {
		log.Fatal("Database token is not set")
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

// ConnectLocal connects to a local SQLite database.
func ConnectLocal() *sql.DB {
	dbName := "local.db"
	dir := "tmp"
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		os.Mkdir(dir, 0755)
	}

	dbPath := filepath.Join(dir, dbName)

	// Open with WAL mode for better concurrency
	db, err := sql.Open("libsql", "file:"+dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("Error opening local db: %v", err)
		os.Exit(1)
	}

	// Set connection pool settings for better concurrency
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	return db
}

// InitializeDB initializes the database connection.
// The local parameter overrides the config.Local setting for backward compatibility.
func InitializeDB(cfg *config.DatabaseConfig, localOverride bool) {
	useLocal := localOverride || cfg.Local

	if !useLocal {
		DB = Connect(cfg)
	} else {
		DB = ConnectLocal()
	}

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

	// Split SQL by semicolon and execute each statement separately
	// This is necessary because libsql's Exec only runs the first statement
	statements := splitSQL(schema)
	log.Printf("Found %d SQL statements to execute", len(statements))

	executedCount := 0
	for i, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			log.Printf("Skipping statement %d/%d (empty)", i+1, len(statements))
			continue
		}

		// Remove comments from the statement for checking
		lines := strings.Split(stmt, "\n")
		var nonCommentLines []string
		for _, line := range lines {
			trimmedLine := strings.TrimSpace(line)
			if trimmedLine != "" && !strings.HasPrefix(trimmedLine, "--") {
				nonCommentLines = append(nonCommentLines, line)
			}
		}

		// If there are no non-comment lines, skip this statement
		if len(nonCommentLines) == 0 {
			log.Printf("Skipping statement %d/%d (comment only)", i+1, len(statements))
			continue
		}

		// Skip vector index creation in local mode (requires Turso)
		if strings.Contains(stmt, "libsql_vector_idx") {
			log.Printf("Skipping statement %d/%d (vector index - requires Turso)", i+1, len(statements))
			continue
		}

		executedCount++
		log.Printf("Executing statement %d/%d (exec #%d): %s...", i+1, len(statements), executedCount, stmt[:min(50, len(stmt))])
		_, err := DB.ExecContext(ctx, stmt)
		if err != nil {
			log.Fatalf("Error creating tables (statement %d): %v\nStatement: %s", i+1, err, stmt[:min(len(stmt), 200)])
			os.Exit(1)
		}
	}
	log.Printf("Successfully created all database tables")
}

func splitSQL(sql string) []string {
	var statements []string
	var currentStatement strings.Builder
	inParenthesis := 0

	for _, ch := range sql {
		currentStatement.WriteRune(ch)

		switch ch {
		case '(':
			inParenthesis++
		case ')':
			inParenthesis--
		case ';':
			if inParenthesis == 0 {
				statements = append(statements, strings.TrimSpace(currentStatement.String()))
				currentStatement.Reset()
			}
		}
	}

	// Add last statement if there is one
	if currentStatement.Len() > 0 {
		statements = append(statements, strings.TrimSpace(currentStatement.String()))
	}

	return statements
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}