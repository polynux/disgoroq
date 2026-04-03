package utils

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tursodatabase/go-libsql"
	_ "github.com/tursodatabase/go-libsql"
	"go.uber.org/zap"

	"polynux/disgoroq/config"
	"polynux/disgoroq/db"
)

var DB *sql.DB
var Q *db.Queries
var dbLogger = zap.NewNop()

func SetLogger(l *zap.Logger) {
	if l != nil {
		dbLogger = l
	}
}

func GetDB() *sql.DB {
	return DB
}

// Connect connects to a remote Turso database using the provided configuration.
func Connect(cfg *config.DatabaseConfig) *sql.DB {
	dbName := "local.db"
	dbUrl := cfg.URL
	dbToken := cfg.Token

	if dbUrl == "" {
		dbLogger.Fatal("Database URL is not set")
		os.Exit(1)
	}
	if dbToken == "" {
		dbLogger.Fatal("Database token is not set")
		os.Exit(1)
	}

	dir, err := os.MkdirTemp("", "libsql-*")
	if err != nil {
		dbLogger.Fatal("Error creating temp directory", zap.Error(err))
		os.Exit(1)
	}

	dbPath := filepath.Join(dir, dbName)

	connector, err := libsql.NewEmbeddedReplicaConnector(dbPath, dbUrl, libsql.WithAuthToken(dbToken), libsql.WithSyncInterval(time.Minute))
	if err != nil {
		dbLogger.Fatal("Error creating database connector", zap.Error(err))
		os.Exit(1)
	}

	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

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
		dbLogger.Fatal("Error opening local database", zap.Error(err))
		os.Exit(1)
	}

	// SQLite handles concurrent writes more reliably with a single shared connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
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
		dbLogger.Fatal("Error reading schema.sql", zap.Error(err))
		os.Exit(1)
	}
	return string(sql)
}

func CreateTables(ctx context.Context) {
	schema := LoadSql()

	// Split SQL by semicolon and execute each statement separately
	// This is necessary because libsql's Exec only runs the first statement
	statements := splitSQL(schema)
	dbLogger.Info("Preparing database schema statements",
		zap.Int("statement_count", len(statements)))

	executedCount := 0
	for i, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			dbLogger.Debug("Skipping empty schema statement",
				zap.Int("index", i+1),
				zap.Int("total", len(statements)))
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
			dbLogger.Debug("Skipping comment-only schema statement",
				zap.Int("index", i+1),
				zap.Int("total", len(statements)))
			continue
		}

		// Skip vector index creation in local mode (requires Turso)
		if strings.Contains(stmt, "libsql_vector_idx") {
			dbLogger.Debug("Skipping Turso-only vector index statement",
				zap.Int("index", i+1),
				zap.Int("total", len(statements)))
			continue
		}

		executedCount++
		dbLogger.Debug("Executing schema statement",
			zap.Int("index", i+1),
			zap.Int("total", len(statements)),
			zap.Int("executed_count", executedCount),
			zap.String("statement_preview", stmt[:min(50, len(stmt))]))
		_, err := DB.ExecContext(ctx, stmt)
		if err != nil {
			dbLogger.Fatal("Error creating database tables",
				zap.Int("statement_index", i+1),
				zap.Error(err),
				zap.String("statement_preview", stmt[:min(len(stmt), 200)]))
			os.Exit(1)
		}
	}
	dbLogger.Info("Database schema initialization complete",
		zap.Int("executed_count", executedCount))
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
