package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sevlyar/go-daemon"

	"github.com/saintbyte/svar-wave/internal/config"
	"github.com/saintbyte/svar-wave/internal/player"
	"github.com/saintbyte/svar-wave/internal/server"
)

var version = "0.1.0"

func main() {
	var (
		configPath = flag.String("config", "config.yaml", "path to YAML config file")
		daemonFlag = flag.Bool("daemon", false, "run as background daemon")
		stopFlag   = flag.Bool("stop", false, "stop the running daemon (via pid file)")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println("svar-wave", version)
		return
	}

	cfgPath := config.Resolve(*configPath)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fatal(err)
	}

	if *stopFlag {
		if err := stopDaemon(cfg.Daemon.PidFile); err != nil {
			fatal(err)
		}
		return
	}

	if *daemonFlag {
		absCfg, _ := filepath.Abs(cfgPath)
		ctx := &daemon.Context{
			PidFileName: cfg.Daemon.PidFile,
			PidFilePerm: 0o644,
			WorkDir:     filepath.Dir(absCfg),
			Umask:       cfg.Daemon.Umask,
		}
		child, err := ctx.Reborn()
		if err != nil {
			fatal(fmt.Errorf("daemonize: %w", err))
		}
		if child != nil { // parent process
			fmt.Printf("svar-wave started, pid %d\n", child.Pid)
			return
		}
		defer func() { _ = ctx.Release() }()
	}

	log := newLogger(cfg, *daemonFlag)

	pl := player.New(cfg.Player.SampleRate, cfg.Player.BufferMS, cfg.Player.Quality, log)
	srv, err := server.New(cfg, pl, log)
	if err != nil {
		log.Error("server init failed", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Listen(); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-errCh:
		log.Error("http server failed", "err", err)
	}

	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	done := make(chan struct{})
	go func() { _ = srv.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-shutdownCtx.Done():
	}
	pl.Stop()
}

func newLogger(cfg *config.Config, daemon bool) *slog.Logger {
	var out *os.File = os.Stdout
	if daemon && cfg.Daemon.LogFile != "" {
		if f, err := os.OpenFile(cfg.Daemon.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			out = f
		} else {
			fmt.Fprintln(os.Stderr, "open log file:", err)
		}
	}
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Log.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	h := slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}

func stopDaemon(pidFile string) error {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return fmt.Errorf("read pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("parse pid file: %w", err)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal pid %d: %w", pid, err)
	}
	fmt.Printf("SIGTERM sent to %d\n", pid)
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
