// Command gacos-scraper runs a daemon that continuously checks an IMAP mailbox
// for GACOS (Generic Atmospheric Correction Online Service for InSAR) download
// emails, downloads the attached .tar.gz archives, and extracts them.
//
// The daemon also submits GACOS requests at startup: the configured date list is
// split into chunks of at most 20 dates (GACOS limit) and each chunk is POSTed
// to http://www.gacos.net/M/action_page.php. After the submission phase it keeps
// polling the mailbox forever until interrupted.
//
// In addition to the daemon, two read-only utility flags are provided:
//   -status  Print a summary of tracked tasks from the SQLite database.
//   -tasks   Print the full list of historical submission tasks.
//
// Usage:
//
//	gacos-scraper --config config.yaml
//
// Options:
//   -config  Path to the YAML configuration file.
//   -dates   Optional text file with additional YYYYMMDD dates.
//   -status  Print current task summary and exit (read-only).
//   -tasks   Print historical task list and exit (read-only).
//   -verbose Enable debug logging.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/xjock/gacos-scraper/internal/config"
	"github.com/xjock/gacos-scraper/internal/orchestrator"
	"github.com/xjock/gacos-scraper/internal/state"
)

// CLI flags.
var (
	configPath = flag.String("config", "config.yaml",
		"Path to the YAML configuration file. See config.example.yaml for the required fields.")
	datesFile = flag.String("dates", "",
		"Optional path to a text file containing SAR acquisition dates, one YYYYMMDD per line. "+
		"If provided, these dates are appended to the dates listed in the config file.")
	status = flag.Bool("status", false,
		"Print a summary of tracked tasks from the SQLite state database and exit. "+
		"Does not submit requests or poll the mailbox.")
	tasks = flag.Bool("tasks", false,
		"Print the list of all historical submission tasks from the SQLite state database and exit. "+
		"For each task the output includes the task ID, dates, status, submission time, retries and last error.")
	verbose = flag.Bool("verbose", false,
		"Enable debug-level logging. Without this flag only INFO, WARN and ERROR messages are shown.")
)

func main() {
	// Replace the default usage message with a concise one.
	flag.Usage = usage
	flag.Parse()

	// Configure structured logging to stderr.
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	// Load and validate configuration.
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Optionally append dates from a separate text file.
	if *datesFile != "" {
		if err := cfg.ParseDatesFile(*datesFile); err != nil {
			slog.Error("failed to load dates file", "error", err)
			os.Exit(1)
		}
	}

	// Ensure output directories (downloads/staging/extracted) exist.
	if err := orchestrator.EnsureOutputDirs(cfg); err != nil {
		slog.Error("failed to create output dirs", "error", err)
		os.Exit(1)
	}

	// Open the SQLite state database. It is created automatically if it does not exist.
	st, err := state.Load(cfg.StateFile())
	if err != nil {
		slog.Error("failed to load state", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	fmt.Fprintf(os.Stderr, "Loaded config: %d dates, output=%s, state=%s\n",
		len(cfg.Gacos.Dates), cfg.Output.Dir, cfg.StateFile())

	// --status: only print summary and exit.
	if *status {
		printStatus(st)
		return
	}

	// --tasks: print the full list of historical submission tasks and exit.
	if *tasks {
		printTasks(st)
		return
	}

	fmt.Fprintf(os.Stderr, "Daemon mode: submitting tasks and checking mailbox every %s. Press Ctrl+C to stop.\n",
		cfg.Polling.Interval)

	// Build the orchestrator and set up graceful shutdown on Ctrl+C / SIGTERM.
	o := orchestrator.New(cfg, st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down")
		cancel()
	}()

	// Run forever (daemon mode).
	if err := o.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("run failed", "error", err)
		os.Exit(1)
	}
}

// usage prints a concise help message.
func usage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
	fmt.Fprintln(os.Stderr, "Daemon that submits GACOS requests and continuously checks email for download links.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Options:")
	flag.PrintDefaults()
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintf(os.Stderr, "  %s --config config.yaml                  # daemon mode\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s --config config.yaml --status         # print progress summary\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s --config config.yaml --tasks          # print historical task list\n", os.Args[0])
}

// printStatus prints a human-readable summary of tasks from the SQLite database.
func printStatus(st *state.State) {
	sum := st.GetSummary()
	fmt.Fprintf(os.Stderr, "State summary (from SQLite database):\n")
	fmt.Fprintf(os.Stderr, "  Submissions: %d total, %d submitted, %d completed, %d failed\n",
		sum.SubmissionsTotal, sum.SubmissionsSubmitted, sum.SubmissionsCompleted, sum.SubmissionsFailed)
	fmt.Fprintf(os.Stderr, "  Downloads:   %d total, %d pending, %d failed\n",
		sum.DownloadsTotal, sum.DownloadsPending, sum.DownloadsFailed)
	fmt.Fprintf(os.Stderr, "  Extractions: %d total, %d failed\n",
		sum.ExtractionsTotal, sum.ExtractionsFailed)
}

// printTasks prints every submission task stored in the SQLite database.
func printTasks(st *state.State) {
	tasks, err := st.GetAllTasks()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list tasks: %v\n", err)
		os.Exit(1)
	}
	if len(tasks) == 0 {
		fmt.Fprintf(os.Stderr, "No tasks found in the state database.\n")
		return
	}

	fmt.Fprintf(os.Stderr, "Historical tasks (%d total):\n", len(tasks))
	for _, t := range tasks {
		submitted := "N/A"
		if !t.SubmittedAt.IsZero() {
			submitted = t.SubmittedAt.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(os.Stderr, "\nID:        %s\n", t.ID)
		fmt.Fprintf(os.Stderr, "Dates:     %s\n", strings.Join(t.Dates, ", "))
		fmt.Fprintf(os.Stderr, "Area:      N=%.4f S=%.4f W=%.4f E=%.4f\n", t.North, t.South, t.West, t.East)
		fmt.Fprintf(os.Stderr, "Time:      %02d:%02d UTC\n", t.Hour, t.Minute)
		fmt.Fprintf(os.Stderr, "Type:      %d\n", t.Type)
		fmt.Fprintf(os.Stderr, "Email:     %s\n", t.Email)
		fmt.Fprintf(os.Stderr, "Status:    %s\n", t.Status)
		fmt.Fprintf(os.Stderr, "Submitted: %s\n", submitted)
		fmt.Fprintf(os.Stderr, "Retries:   %d\n", t.Retries)
		if t.LastError != "" {
			fmt.Fprintf(os.Stderr, "LastError: %s\n", t.LastError)
		}
	}
}
