// openldap-exporter exposes OpenLDAP health, cn=Monitor statistics and
// syncrepl replication state to Prometheus.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/VibhuviOiO/openldap-exporter/internal/collector"
	"github.com/VibhuviOiO/openldap-exporter/internal/config"
)

func main() {
	var (
		cfgPath  = flag.String("config", "openldap-exporter.yml", "path to the YAML config")
		listen   = flag.String("listen", "", "override listen address from config, e.g. :9330")
		logLevel = flag.String("log.level", "info", "debug | info | warn | error")
		logFmt   = flag.String("log.format", "text", "text | json")
		check    = flag.Bool("check", false, "load the config, print a summary, exit")
		version  = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *version {
		fmt.Printf("openldap-exporter %s (%s)\n", collector.Version, collector.Revision)
		return
	}

	log := newLogger(*logLevel, *logFmt)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}

	if *check {
		fmt.Printf("listen        %s%s\n", cfg.Listen, cfg.MetricsPath)
		fmt.Printf("scrape_timeout %s\n", cfg.ScrapeTimeout)
		fmt.Printf("targets       %d\n", len(cfg.Targets))
		for _, t := range cfg.Targets {
			cfgAccess := "no"
			if t.ConfigBindDN != "" {
				cfgAccess = t.ConfigBindDN
			}
			fmt.Printf("  %-18s %-45s group=%-8s role=%-8s cn=config: %s\n", t.Name, t.URI, t.Group, t.Role, cfgAccess)
		}
		fmt.Printf("suffixes      %v (empty = discover from namingContexts)\n", cfg.Suffixes)
		fmt.Printf("entry counts  %d\n", len(cfg.Entries))
		for _, e := range cfg.Entries {
			fmt.Printf("  %-18s %s  %s  scope=%s\n", e.Name, e.Base, e.Filter, e.Scope)
		}
		fmt.Printf("in_sync_tolerance %s\n", cfg.Replication.InSyncTolerance)
		return
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	reg.MustRegister(collector.New(cfg, log))

	mux := http.NewServeMux()
	mux.Handle(cfg.MetricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorLog:      slog.NewLogLogger(log.Handler(), slog.LevelError),
		ErrorHandling: promhttp.ContinueOnError,
		Timeout:       cfg.ScrapeTimeout + 5*time.Second,
	}))

	// /probe?target=<name> scrapes one configured target. Only configured
	// names are accepted: this exporter holds credentials, so it must never
	// be pointed at an arbitrary host by a URL parameter.
	mux.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("target")
		t := cfg.TargetByName(name)
		if t == nil {
			http.Error(w, "unknown target; must be a name from the config", http.StatusBadRequest)
			return
		}
		sub := *cfg
		sub.Targets = []config.Target{*t}
		pr := prometheus.NewRegistry()
		pr.MustRegister(collector.New(&sub, log))
		promhttp.HandlerFor(pr, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, landing, cfg.MetricsPath, len(cfg.Targets), collector.Version)
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", cfg.Listen, "metrics", cfg.MetricsPath, "targets", len(cfg.Targets), "version", collector.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Info("stopped")
}

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

const landing = `<!doctype html><title>openldap-exporter</title>
<style>body{font:15px/1.5 system-ui;max-width:42em;margin:3em auto;padding:0 1em;color:#222}code{background:#f3f3f3;padding:.1em .3em}</style>
<h1>openldap-exporter</h1>
<p><a href="%s">metrics</a> &middot; %d targets &middot; version %s</p>
<p><code>/probe?target=&lt;name&gt;</code> scrapes one configured target.<br>
<code>/healthz</code> answers 200 while the process is up.</p>
<p>Replication lag is derived from <code>contextCSN</code> compared across every target in the same
<code>group</code>. Provider-side link presence is read from each provider's <code>cn=Connections</code>.</p>
`
