// Command dispatcher drives a Cloud Native Airlines tour: it keeps a run's
// schedule synced with Operations and launches one ephemeral Flight container per
// leg as simulated time reaches each departure.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloud-native-airlines/dispatcher/internal/config"
	"github.com/cloud-native-airlines/dispatcher/internal/controller"
	"github.com/cloud-native-airlines/dispatcher/internal/launcher"
	"github.com/cloud-native-airlines/dispatcher/internal/operations"
	"github.com/cloud-native-airlines/dispatcher/internal/sim"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	lnch, err := launcher.New()
	if err != nil {
		log.Error("docker launcher error", "error", err)
		os.Exit(1)
	}
	defer lnch.Close()

	ctrl := controller.New(cfg, operations.New(cfg.OperationsURL), lnch, log)

	log.Info("dispatcher starting",
		"aircraft_id", cfg.AircraftID, "flight_image", cfg.FlightImage,
		"flight_network", cfg.FlightNetwork, "operations_url", cfg.OperationsURL)

	// Health endpoint.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	srv := &http.Server{Addr: cfg.ListenAddr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "error", err)
			stop()
		}
	}()

	// Initialize from the current clock so the run's schedule exists immediately,
	// even before anyone presses play.
	if clock, err := sim.FetchClock(ctx, cfg.SimURL); err != nil {
		log.Warn("initial clock fetch failed; will rely on ticks", "error", err)
	} else {
		ctrl.OnClock(ctx, clock)
	}

	// Primary driver: Simulator ticks over NATS.
	var sub *sim.Subscriber
	if cfg.NATSURL != "" {
		sub, err = sim.Subscribe(cfg.NATSURL, cfg.TickSubject, log, func(c sim.Clock) { ctrl.OnClock(ctx, c) })
		if err != nil {
			log.Error("tick subscriber error", "error", err)
			os.Exit(1)
		}
		defer sub.Close()
	}

	// Fallback poll: catches a run reset while paused (no ticks flow then) and
	// keeps the schedule current if ticks are sparse.
	go func() {
		ticker := time.NewTicker(cfg.PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if clock, err := sim.FetchClock(ctx, cfg.SimURL); err == nil {
					ctrl.OnClock(ctx, clock)
				}
			}
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown error", "error", err)
	}
}
