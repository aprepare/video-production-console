package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	root, err := dataRoot(args)
	if err != nil {
		return err
	}
	database := filepath.Join(root, "console.db")
	info, err := os.Stat(database)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no existing database; first run will create one")
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("database is not a file: %s", database)
	}
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return err
	}
	defer db.Close()
	cleared, err := clearStaleOptionalSettings(db)
	if err != nil {
		return err
	}
	if len(cleared) == 0 {
		fmt.Println("startup settings already safe")
		return nil
	}
	fmt.Printf("cleared stale settings so the console can start: %v\n", cleared)
	return nil
}

func dataRoot(args []string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return filepath.Clean(args[0]), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "video-console-data"), nil
}

func clearStaleOptionalSettings(db *sql.DB) ([]string, error) {
	keys := []string{"ffmpeg_path", "ffprobe_path", "machine_profile_path"}
	cleared := make([]string, 0, len(keys))
	for _, key := range keys {
		result, err := db.Exec(`UPDATE settings SET value='' WHERE key=? AND TRIM(value) != ''`, key)
		if err != nil {
			return nil, fmt.Errorf("clear %s: %w", key, err)
		}
		n, _ := result.RowsAffected()
		if n > 0 {
			cleared = append(cleared, key)
		}
	}
	return cleared, nil
}
