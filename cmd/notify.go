package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/benenen/myclaw/internal/config"
	"github.com/benenen/myclaw/internal/store"
	"github.com/benenen/myclaw/internal/store/repositories"
)

const currentRunIDFileName = ".myclaw-run-id"

func NewNotifyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "notify <runtime> <botname>",
		Short: "Mark the current agent run as done",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.New(cmd.ErrOrStderr(), "", log.LstdFlags)
			runID, err := readCurrentRunID()
			if err != nil {
				return err
			}
			logger.Printf("notify start: runtime=%s bot=%s run_id=%s", args[0], args[1], runID)
			paths, err := config.LoadDataPaths()
			if err != nil {
				return err
			}
			if paths.DataDir != "" {
				if err := os.MkdirAll(paths.DataDir, 0o755); err != nil {
					return err
				}
			}
			if paths.SQLitePath != "" && paths.SQLitePath != ":memory:" {
				if err := os.MkdirAll(filepath.Dir(paths.SQLitePath), 0o755); err != nil {
					return err
				}
			}

			db, err := store.Open(paths.SQLitePath)
			if err != nil {
				return err
			}
			if err := store.Migrate(db); err != nil {
				return err
			}

			repo := repositories.NewAgentRunRepository(db)
			if err := repo.UpsertDone(context.Background(), runID, args[1], args[0]); err != nil {
				return err
			}
			logger.Printf("notify done: runtime=%s bot=%s run_id=%s", args[0], args[1], runID)
			return nil
		},
	}
}

func readCurrentRunID() (string, error) {
	payload, err := os.ReadFile(currentRunIDFileName)
	if err != nil {
		return "", fmt.Errorf("read current run id: %w", err)
	}
	runID := strings.TrimSpace(string(payload))
	if runID == "" {
		return "", fmt.Errorf("current run id is empty")
	}
	return runID, nil
}
