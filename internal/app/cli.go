package app

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

func (a *App) Run(args []string) error {
	if len(args) == 0 {
		a.printUsage()
		return nil
	}

	switch args[0] {
	case "backup":
		fs := flag.NewFlagSet("backup", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("backup does not accept positional arguments")
		}
		name, count, err := a.Backup()
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "Backup: %s\nFiles: %d\n", name, count)
	case "revert":
		fs := flag.NewFlagSet("revert", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		provider := fs.String("provider", "", "override target model_provider")
		workers := fs.Int("workers", a.Workers, "number of concurrent session-file workers")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("revert does not accept positional arguments")
		}
		if *workers < 1 {
			return errors.New("workers must be greater than 0")
		}
		a.Workers = *workers
		target := strings.TrimSpace(*provider)
		if target == "" {
			var configured bool
			var err error
			target, configured, err = a.ConfigModelProvider()
			if err != nil {
				return err
			}
			if configured {
				fmt.Fprintf(a.Out, "Target model_provider: %s (from ~/.codex/config.toml)\n", target)
			} else {
				fmt.Fprintf(a.Out, "Target model_provider: %s (default)\n", target)
			}
		} else {
			fmt.Fprintf(a.Out, "Target model_provider: %s (from --provider)\n", target)
		}
		name, files, changedLines, err := a.Revert(target)
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "Backup: %s\nFiles scanned: %d\nmodel_provider fields updated: %d\n", name, files, changedLines)
	case "restore":
		fs := flag.NewFlagSet("restore", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("restore requires exactly one backup name")
		}
		resolvedName, count, err := a.Restore(fs.Arg(0))
		if err != nil {
			return err
		}
		fmt.Fprintf(a.Out, "Restored backup: %s\nFiles restored: %d\n", resolvedName, count)
	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		workers := fs.Int("workers", a.Workers, "number of concurrent session-file workers")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("status does not accept positional arguments")
		}
		if *workers < 1 {
			return errors.New("workers must be greater than 0")
		}
		a.Workers = *workers
		return a.Status()
	case "clean":
		fs := flag.NewFlagSet("clean", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		dryRun := fs.Bool("dry-run", false, "show matching backups without deleting them")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("clean does not accept positional arguments")
		}
		count, names, err := a.Clean(*dryRun)
		if err != nil {
			return err
		}
		if *dryRun {
			fmt.Fprintf(a.Out, "Matching backups: %d\n", count)
		} else {
			fmt.Fprintf(a.Out, "Deleted backups: %d\n", count)
		}
		for _, name := range names {
			fmt.Fprintf(a.Out, "- %s\n", name)
		}
	case "-h", "--help", "help":
		a.printUsage()
	default:
		a.printUsage()
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}

func (a *App) printUsage() {
	fmt.Fprintln(a.Out, `Usage:
  codex-session-revert backup
  codex-session-revert revert [--provider openai] [--workers N]
  codex-session-revert restore <backup-name>
  codex-session-revert status [--workers N]
  codex-session-revert clean [--dry-run]`)
}
