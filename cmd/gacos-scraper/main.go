// Command gacos-scraper runs a daemon that continuously checks an IMAP mailbox
// for GACOS (Generic Atmospheric Correction Online Service for InSAR) download
// emails, downloads the attached .tar.gz archives, and extracts them.
//
// The daemon also submits GACOS requests at startup: the configured date list is
// split into chunks of at most 20 dates (GACOS limit) and each chunk is POSTed
// to http://www.gacos.net/M/action_page.php. After the submission phase it keeps
// polling the mailbox forever until interrupted.
//
// Usage:
//
//	gacos-scraper --config config.yaml
//
// Options:
//   -config  Path to the YAML configuration file.
//   -dates   Optional text file with additional YYYYMMDD dates.
//   -verbose Enable debug logging.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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
	fmt.Fprintf(os.Stderr, "Example:\n  %s --config config.yaml\n", os.Args[0])
}
