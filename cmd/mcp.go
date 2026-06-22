package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/benenen/myclaw/internal/app/mcpserver"
	"github.com/benenen/myclaw/internal/config"
	"github.com/benenen/myclaw/internal/domain"
	"github.com/benenen/myclaw/internal/store"
	"github.com/benenen/myclaw/internal/store/repositories"
)

func NewMCPCommand(stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers that agents can connect to",
	}
	cmd.AddCommand(newMCPListCommand(stderr))
	cmd.AddCommand(newMCPAddCommand(stderr))
	cmd.AddCommand(newMCPRemoveCommand(stderr))
	cmd.AddCommand(newMCPEnableCommand(stderr))
	cmd.AddCommand(newMCPDisableCommand(stderr))
	cmd.AddCommand(newMCPAttachCommand(stderr))
	cmd.AddCommand(newMCPDetachCommand(stderr))
	return cmd
}

func newMCPService(stderr io.Writer) (*mcpserver.Service, func(), error) {
	logger := log.New(stderr, "", 0)
	paths, err := config.LoadDataPaths()
	if err != nil {
		return nil, nil, fmt.Errorf("load data paths: %w", err)
	}
	if paths.SQLitePath != "" && paths.SQLitePath != ":memory:" {
		dir := strings.TrimSuffix(paths.SQLitePath, "/myclaw.db")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Printf("warn: create data dir: %v", err)
		}
	}
	db, err := store.Open(paths.SQLitePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	if err := store.Migrate(db); err != nil {
		return nil, nil, fmt.Errorf("migrate database: %w", err)
	}
	svc := mcpserver.NewService(repositories.NewMCPServerRepository(db), repositories.NewBotRepository(db))
	cleanup := func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	return svc, cleanup, nil
}

func newMCPListCommand(stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [--bot <botID>]",
		Short: "List all MCP servers, or a single bot's attached servers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, cleanup, err := newMCPService(stderr)
			if err != nil {
				return err
			}
			defer cleanup()

			botID, _ := cmd.Flags().GetString("bot")
			var servers []domain.MCPServer
			if strings.TrimSpace(botID) != "" {
				servers, err = svc.ListByBot(cmd.Context(), botID)
			} else {
				servers, err = svc.List(cmd.Context())
			}
			if err != nil {
				return fmt.Errorf("list mcp servers: %w", err)
			}
			if len(servers) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no MCP servers configured)")
				return nil
			}
			for _, s := range servers {
				status := "enabled"
				if !s.Enabled {
					status = "disabled"
				}
				line := fmt.Sprintf("%-24s %-8s %-8s", s.Name, s.ServerType, status)
				if s.ServerType == mcpserver.TypeHTTP {
					line += fmt.Sprintf(" %s", s.URL)
				} else {
					line += fmt.Sprintf(" %s %s", s.Command, strings.Join(s.Args, " "))
				}
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
	cmd.Flags().String("bot", "", "show only servers attached to this bot id")
	return cmd
}

func newMCPAddCommand(stderr io.Writer) *cobra.Command {
	var (
		serverType string
		url        string
		command    string
		args       []string
	)
	cmd := &cobra.Command{
		Use:   "add --name <name> [--type http|stdio] [--url <url>] [--command <cmd>] [--args a1,a2]",
		Short: "Add an MCP server configuration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _ := cmd.Flags().GetString("name")
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("--name is required")
			}
			svc, cleanup, err := newMCPService(stderr)
			if err != nil {
				return err
			}
			defer cleanup()
			server, err := svc.Create(cmd.Context(), mcpserver.CreateInput{
				Name: name, ServerType: serverType, URL: url, Command: command, Args: args,
			})
			if err != nil {
				return fmt.Errorf("create mcp server: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created MCP server %q (type=%s)\n", server.Name, server.ServerType)
			return nil
		},
	}
	cmd.Flags().String("name", "", "server name (required)")
	cmd.Flags().StringVar(&serverType, "type", mcpserver.TypeHTTP, "server type: http or stdio")
	cmd.Flags().StringVar(&url, "url", "", "server URL (for http type)")
	cmd.Flags().StringVar(&command, "command", "", "command to run (for stdio type)")
	cmd.Flags().StringSliceVar(&args, "args", nil, "comma-separated args (for stdio type)")
	return cmd
}

func newMCPRemoveCommand(stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an MCP server (also detaches it from all bots)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := newMCPService(stderr)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := svc.Remove(cmd.Context(), args[0]); err != nil {
				return fmt.Errorf("remove mcp server: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed MCP server %q\n", args[0])
			return nil
		},
	}
}

func newMCPEnableCommand(stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable an MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := newMCPService(stderr)
			if err != nil {
				return err
			}
			defer cleanup()
			if _, err := svc.SetEnabled(cmd.Context(), args[0], true); err != nil {
				return fmt.Errorf("enable mcp server: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "enabled MCP server %q\n", args[0])
			return nil
		},
	}
}

func newMCPDisableCommand(stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable an MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := newMCPService(stderr)
			if err != nil {
				return err
			}
			defer cleanup()
			if _, err := svc.SetEnabled(cmd.Context(), args[0], false); err != nil {
				return fmt.Errorf("disable mcp server: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "disabled MCP server %q\n", args[0])
			return nil
		},
	}
}

func newMCPAttachCommand(stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach --bot <botID> --server <name>",
		Short: "Attach an MCP server to a bot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			botID, _ := cmd.Flags().GetString("bot")
			server, _ := cmd.Flags().GetString("server")
			if strings.TrimSpace(botID) == "" || strings.TrimSpace(server) == "" {
				return fmt.Errorf("--bot and --server are required")
			}
			svc, cleanup, err := newMCPService(stderr)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := svc.AttachToBot(cmd.Context(), botID, server); err != nil {
				return fmt.Errorf("attach: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "attached %q to bot %q\n", server, botID)
			return nil
		},
	}
	cmd.Flags().String("bot", "", "bot id (required)")
	cmd.Flags().String("server", "", "mcp server name (required)")
	return cmd
}

func newMCPDetachCommand(stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detach --bot <botID> --server <name>",
		Short: "Detach an MCP server from a bot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			botID, _ := cmd.Flags().GetString("bot")
			server, _ := cmd.Flags().GetString("server")
			if strings.TrimSpace(botID) == "" || strings.TrimSpace(server) == "" {
				return fmt.Errorf("--bot and --server are required")
			}
			svc, cleanup, err := newMCPService(stderr)
			if err != nil {
				return err
			}
			defer cleanup()
			if err := svc.DetachFromBot(cmd.Context(), botID, server); err != nil {
				return fmt.Errorf("detach: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "detached %q from bot %q\n", server, botID)
			return nil
		},
	}
	cmd.Flags().String("bot", "", "bot id (required)")
	cmd.Flags().String("server", "", "mcp server name (required)")
	return cmd
}
