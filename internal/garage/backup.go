package garage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
)

type sqliteBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

// Backup preserves the source schema, including committed WAL data. It must not
// call Open: backups taken before an upgrade need the unmigrated database.
func Backup(ctx context.Context, source, destination string) (err error) {
	ctx, done := operation(ctx, "db.backup")
	defer func() { done(err) }()
	if err = ctx.Err(); err != nil {
		return err
	}
	if source == "" || destination == "" {
		return errors.New("source and destination are required")
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve backup source: %w", err)
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("backup source must be an existing regular file")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("resolve backup destination directory: %w", err)
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return err
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	for _, reserved := range []string{source, source + "-wal", source + "-shm", source + "-journal"} {
		if destination == reserved {
			return errors.New("backup destination must not replace the source or its journals")
		}
	}
	if _, err = os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("backup destination already exists")
		}
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	workspace, err := os.MkdirTemp(parent, ".pitpilot-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)
	temporary := filepath.Join(workspace, "snapshot.db")
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = snapshotSQLite(ctx, source, temporary); err != nil {
		return err
	}
	if err = verifySQLiteBackup(ctx, temporary); err != nil {
		return err
	}
	file, err = os.OpenFile(temporary, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = errors.Join(file.Sync(), file.Close())
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Linking within one filesystem publishes the complete file atomically and
	// fails if another caller created the destination after the initial check.
	return os.Link(temporary, destination)
}

func sqliteFileURI(filename, mode string) string {
	u := url.URL{Scheme: "file", Path: filename}
	query := url.Values{"mode": {mode}, "_pragma": {"busy_timeout(5000)"}}
	u.RawQuery = query.Encode()
	return u.String()
}

func snapshotSQLite(ctx context.Context, source, destination string) error {
	database, err := sql.Open("sqlite", sqliteFileURI(source, "ro"))
	if err != nil {
		return err
	}
	defer database.Close()
	connection, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Raw(func(driverConnection any) (err error) {
		backuper, ok := driverConnection.(sqliteBackuper)
		if !ok {
			return errors.New("SQLite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(sqliteFileURI(destination, "rw"))
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, backup.Finish()) }()
		for {
			if err = ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(128)
			if err != nil || !more {
				return err
			}
		}
	})
}

func verifySQLiteBackup(ctx context.Context, filename string) error {
	database, err := sql.Open("sqlite", sqliteFileURI(filename, "ro"))
	if err != nil {
		return err
	}
	defer database.Close()
	var result string
	if err = database.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("backup failed SQLite integrity check")
	}
	return nil
}
