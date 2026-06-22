package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/benenen/myclaw/cmd"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWithServer(args, stdout, stderr, cmd.RunServer)
}

func runWithServer(args []string, stdout, stderr io.Writer, server func(io.Writer) int) int {
	root, exitCode := newRootCommand(stdout, stderr, server)
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		if len(args) > 0 && !isHelpArg(args[0]) {
			for _, command := range root.Commands() {
				if command.Name() == args[0] {
					fmt.Fprintln(stderr, err)
					return 1
				}
			}

			fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
			writeUsage(stderr)
			return 1
		}

		fmt.Fprintln(stderr, err)
		return 1
	}

	return *exitCode
}

func newRootCommand(stdout, stderr io.Writer, server func(io.Writer) int) (*cobra.Command, *int) {
	exitCode := 0

	root := &cobra.Command{
		Use:           "myclaw",
		SilenceErrors: true,
		SilenceUsage:  true,
		Run: func(_ *cobra.Command, _ []string) {
			exitCode = server(stderr)
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetUsageFunc(func(cmd *cobra.Command) error {
		writeUsage(cmd.OutOrStdout())
		return nil
	})

	root.AddCommand(cmd.NewServerCommandWithRunner(stderr, &exitCode, server))
	root.AddCommand(cmd.NewMCPCommand(stderr))

	return root, &exitCode
}

func isHelpArg(arg string) bool {
	switch arg {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func writeUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  myclaw [server]")
	fmt.Fprintln(w, "  myclaw help")
	fmt.Fprintln(w, "  myclaw mcp <list|add|remove|enable|disable|attach|detach> [...]")
}
