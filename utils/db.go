package utils

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
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

	db, err := openLocalDB(filepath.Join(dir, dbName))
	if err != nil {
		dbLogger.Fatal("Error opening local database", zap.Error(err))
		os.Exit(1)
	}

	return db
}

// localBusyTimeoutMS is how long a statement waits for a lock held by another
// connection before failing with "database is locked".
const localBusyTimeoutMS = 5000

// openLocalDB opens a local libsql database file with one long-lived connection.
//
// go-libsql hands the file: URL to SQLite unchanged, so mattn-style query
// parameters such as _busy_timeout are ignored. The pragmas therefore run on
// every new connection through pragmaConnector instead.
func openLocalDB(dbPath string) (*sql.DB, error) {
	base, err := sql.Open("libsql", "file:"+dbPath)
	if err != nil {
		return nil, err
	}
	driverCtx, ok := base.Driver().(driver.DriverContext)
	if !ok {
		base.Close()
		return nil, fmt.Errorf("libsql driver does not support connectors")
	}
	_ = base.Close()

	connector, err := driverCtx.OpenConnector("file:" + dbPath)
	if err != nil {
		return nil, err
	}

	db := sql.OpenDB(pragmaConnector{
		Connector: connector,
		pragmas: []string{
			fmt.Sprintf("PRAGMA busy_timeout = %d", localBusyTimeoutMS),
			"PRAGMA journal_mode = WAL",
		},
	})

	// SQLite handles concurrent writes more reliably with a single shared
	// connection. Keep it open: recycling it makes the closing connection
	// checkpoint the WAL while its replacement opens.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	return db, nil
}

// pragmaConnector runs per-connection pragmas on every connection it opens.
type pragmaConnector struct {
	driver.Connector
	pragmas []string
}

func (c pragmaConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	queryer, ok := conn.(driver.QueryerContext)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("libsql connection does not support queries")
	}
	for _, pragma := range c.pragmas {
		rows, err := queryer.QueryContext(ctx, pragma, nil)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
		rows.Close()
	}
	return conn, nil
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
