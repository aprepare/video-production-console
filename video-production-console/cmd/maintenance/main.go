package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"video-production-console/internal/store"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: console-maintenance <backup|check|restore>")
	}
	set := flag.NewFlagSet(args[0], flag.ContinueOnError)
	switch args[0] {
	case "backup":
		root := set.String("data-root", "", "console data root")
		output := set.String("output", "", "backup directory outside data root")
		if err := set.Parse(args[1:]); err != nil || *root == "" || *output == "" {
			return fmt.Errorf("backup requires -data-root and -output")
		}
		db, err := store.Open(filepath.Join(*root, "console.db"))
		if err != nil {
			return err
		}
		defer db.Close()
		return store.CreateBackup(db, *root, *output, time.Now().UTC())
	case "check":
		database := set.String("database", "", "SQLite database path")
		if err := set.Parse(args[1:]); err != nil || *database == "" {
			return fmt.Errorf("check requires -database")
		}
		return store.CheckIntegrity(ctx, *database)
	case "restore":
		backup := set.String("backup", "", "SQLite backup path")
		destination := set.String("destination", "", "new database path")
		if err := set.Parse(args[1:]); err != nil || *backup == "" || *destination == "" {
			return fmt.Errorf("restore requires -backup and -destination")
		}
		return store.RestoreBackup(ctx, *backup, *destination)
	default:
		return fmt.Errorf("unknown maintenance command %q", args[0])
	}
}
