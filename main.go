package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"boot.dev/linko/internal/store"
)

func main() {

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	httpPort := flag.Int("port", 8899, "port to listen on")
	dataDir := flag.String("data", "./data", "directory to store data")
	flag.Parse()

	status := run(ctx, cancel, *httpPort, *dataDir)
	cancel()
	os.Exit(status)
}

// create a close function to prevent a bug when buffering logger
type closeFunc func() error
var initCloserFunc closeFunc
func initializeLogger(logFile string) (*slog.Logger, closeFunc, error) {
	
	// infoHandler := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if logFile == "" {
		initCloserFunc = func() error { return nil }
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})), initCloserFunc, nil
	}
	f, fileErr := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if fileErr != nil {
		return nil, func() error { return nil }, fmt.Errorf("failed to open log file: %v", fileErr)
	}
	bufferedFile := bufio.NewWriterSize(f, 8192)
	multiWriter := io.MultiWriter(os.Stderr, bufferedFile)

	// debugHandler := slog.New(slog.NewTextHandler(multiWriter, &slog.HandlerOptions{Level: slog.LevelDebug}))
	initCloserFunc = func() error {
		bufferErr := bufferedFile.Flush()
		if bufferErr != nil {
			return fmt.Errorf("Error closing the buffer: %v", bufferErr)
		}
		fileErr := f.Close()
		if fileErr != nil {
			return fmt.Errorf("Error closing the file: %v", fileErr)
		}
		return nil
	}
	return slog.New(slog.NewTextHandler(multiWriter, &slog.HandlerOptions{Level: slog.LevelDebug})), initCloserFunc, nil
}

func run(ctx context.Context, cancel context.CancelFunc, httpPort int, dataDir string) int {
	logFile := os.Getenv("LINKO_LOG_FILE")
	logger, closeFunc, loggerErr := initializeLogger(logFile)

	defer func() {
		err := closeFunc()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}()
	if loggerErr != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", loggerErr)
		return 1
	}
	st, err := store.New(dataDir, logger)
	if err != nil {
		logger.Info(fmt.Sprintf("failed to create store: %v", err))
		return 1
	}
	s := newServer(*st, httpPort, cancel, logger)
	var serverErr error
	go func() {
		serverErr = s.start()
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.shutdown(shutdownCtx); err != nil {
		logger.Info(fmt.Sprintf("failed to shutdown server: %v", err))
		return 1
	}
	if serverErr != nil {
		logger.Info(fmt.Sprintf("server error: %v", serverErr))
		return 1
	}
	return 0
}
