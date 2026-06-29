package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const driverName = "sqlite"

// DB — обёртка над sqlite-коннектом.
// Используем одно соединение, чтобы не ловить SQLITE_BUSY.
type DB struct {
	conn *sql.DB
}

// Open открывает базу и применяет ожидающие миграции.
func Open(dbPath string) (*DB, error) {
	dsn := dbPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	conn, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	conn.SetMaxOpenConns(1)
	conn.SetConnMaxLifetime(time.Hour)

	ctx := context.Background()
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	d := &DB{conn: conn}
	if err := d.migrate(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return d, nil
}

// Close закрывает соединение с базой.
func (d *DB) Close() error {
	return d.conn.Close()
}

// withTx выполняет fn внутри транзакции.
func (d *DB) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

type migration struct {
	version int64
	name    string
	sql     string
}

func (d *DB) ensureMigrationTable(ctx context.Context) error {
	_, err := d.conn.ExecContext(ctx, `
        CREATE TABLE IF NOT EXISTS schema_migrations (
            version INTEGER PRIMARY KEY,
            applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
        )
    `)
	return err
}

func (d *DB) migrationApplied(ctx context.Context, version int64) (bool, error) {
	var n int
	err := d.conn.QueryRowContext(ctx,
		"SELECT 1 FROM schema_migrations WHERE version = ?",
		version,
	).Scan(&n)

	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (d *DB) migrate(ctx context.Context) error {
	if err := d.ensureMigrationTable(ctx); err != nil {
		return err
	}

	files, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var migrations []migration
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}

		parts := strings.SplitN(f.Name(), "_", 2)
		ver, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return fmt.Errorf("bad migration name %q: %w", f.Name(), err)
		}

		b, err := migrationsFS.ReadFile(path.Join("migrations", f.Name()))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f.Name(), err)
		}

		migrations = append(migrations, migration{
			version: ver,
			name:    f.Name(),
			sql:     string(b),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	for _, m := range migrations {
		applied, err := d.migrationApplied(ctx, m.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		if err := d.withTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("exec %s: %w", m.name, err)
			}
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO schema_migrations (version) VALUES (?)",
				m.version,
			); err != nil {
				return fmt.Errorf("mark %s: %w", m.name, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}
