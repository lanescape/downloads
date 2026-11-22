// Command downloads is the service, on Cloud Run (specs/0001-service.md):
// it redirects https://downloads.lanescape.dev/<group>/<key> to <key> at the
// group's provider.
//
// Its configuration is embedded (config/groups.yaml). What differs by where
// it runs comes from the environment:
//
//	PORT                   where to listen, 8080 unless set; Cloud Run sets it
//	DOWNLOADS_SECRETS_DIR  where the secrets are mounted, each at <dir>/<name>/value; /secrets unless set
//	DOWNLOADS_SECRETS_FILE a YAML or JSON file mapping secrets' names to their values, read instead
//	                       of DOWNLOADS_SECRETS_DIR, for running it outside Cloud Run
//	DOWNLOADS_R2_ENDPOINT  an endpoint to use for every R2 group instead of the account's,
//	                       such as the emulator's (cmd/r2-emulator); never set on Cloud Run
//
// It refuses to start unless every secret the configuration names has a
// value, and names any that hasn't.
//
// With -secret-names, it prints the names of the secrets the configuration
// names, comma-separated, and exits: CD passes them to infra/, which makes
// each one in Secret Manager and mounts it. With -smoke-check <URL>, it
// checks the service deployed at the URL, as CD does after each deploy, and
// exits (smoke.go).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lanescape/downloads/config"
	"github.com/lanescape/downloads/internal/app"
	"github.com/lanescape/downloads/internal/secrets"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, log, nil); err != nil {
		log.Error("downloads stopped", "err", err)
		os.Exit(1)
	}
}

// run runs the service until ctx ends. ready, if set, is given the address
// it listens on.
func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer, log *slog.Logger, ready func(net.Addr)) error {
	flags := flag.NewFlagSet("downloads", flag.ContinueOnError)
	flags.SetOutput(stdout)
	secretNames := flags.Bool("secret-names", false, "print the configuration's secrets' names, comma-separated, and exit")
	smokeURL := flags.String("smoke-check", "", "check the service deployed at this URL, and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	if *secretNames {
		_, err := fmt.Fprintln(stdout, strings.Join(c.SecretNames(), ","))
		return err
	}
	if *smokeURL != "" {
		return smoke(ctx, c, *smokeURL, http.DefaultTransport, stdout, r2Endpoint)
	}

	var src secrets.Source
	if path := getenv("DOWNLOADS_SECRETS_FILE"); path != "" {
		if src, err = secrets.File(path); err != nil {
			return err
		}
	} else {
		dir := getenv("DOWNLOADS_SECRETS_DIR")
		if dir == "" {
			dir = "/secrets"
		}
		src = secrets.Dir(dir)
	}
	h, err := app.New(c, app.Options{Secrets: src, R2Endpoint: getenv("DOWNLOADS_R2_ENDPOINT"), Log: log})
	if err != nil {
		return err
	}
	port := getenv("PORT")
	if port == "" {
		port = "8080"
	}
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		// OPTIONS * reaches the handler, which refuses it, rather than
		// net/http answering it.
		DisableGeneralOptionsHandler: true,
		ErrorLog:                     slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("downloads listening", "addr", ln.Addr().String(), "groups", len(c.Groups))
	if ready != nil {
		ready(ln.Addr())
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	// Cloud Run gives an instance 10 seconds after SIGTERM.
	shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
