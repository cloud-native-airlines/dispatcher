// Package controller is the dispatcher's brain: it keeps a run's schedule in
// sync with Operations and launches one Flight per leg as simulated time reaches
// each leg's scheduled departure. Because the schedule already spaces legs by
// block + turnaround, launching at departure naturally sequences the tour.
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cloud-native-airlines/dispatcher/internal/config"
	"github.com/cloud-native-airlines/dispatcher/internal/launcher"
	"github.com/cloud-native-airlines/dispatcher/internal/operations"
	"github.com/cloud-native-airlines/dispatcher/internal/sim"
)

// legLauncher is the subset of *launcher.Launcher the controller needs (an
// interface so it can be faked in tests).
type legLauncher interface {
	Launch(ctx context.Context, s launcher.Spec) (string, error)
}

type leg struct {
	instanceID   string
	flightNumber string
	aircraftID   string
	origin       operations.Airport
	destination  operations.Airport
	departure    time.Time
	arrival      time.Time
}

type Controller struct {
	cfg      config.Config
	ops      *operations.Client
	launcher legLauncher
	log      *slog.Logger

	mu        sync.Mutex
	runID     string
	legs      []leg
	launched  map[string]bool
	airports  map[string]operations.Airport
}

func New(cfg config.Config, ops *operations.Client, l legLauncher, log *slog.Logger) *Controller {
	return &Controller{cfg: cfg, ops: ops, launcher: l, log: log, launched: map[string]bool{}}
}

// OnClock reacts to a new clock reading: (re)builds the schedule when the run
// changes, then launches any legs now due. Safe to call from the tick callback
// and a fallback poll.
func (c *Controller) OnClock(ctx context.Context, clock sim.Clock) {
	if clock.RunID == "" {
		return
	}
	c.mu.Lock()
	needRebuild := clock.RunID != c.runID || len(c.legs) == 0
	c.mu.Unlock()

	if needRebuild {
		if err := c.rebuild(ctx, clock.RunID, clock.SimulatedAt); err != nil {
			c.log.Error("schedule rebuild failed; will retry", "run_id", clock.RunID, "error", err)
			return
		}
	}
	// Keep the schedule synced even while paused, but only launch legs when the
	// clock is running — so nothing flies until someone presses play, and a late
	// pause doesn't strand a half-dispatched tour.
	if !clock.Paused {
		c.drive(ctx, clock.SimulatedAt)
	}
}

// rebuild initializes the run in Operations and loads its schedule for our
// aircraft, resolving airport coordinates for each leg.
func (c *Controller) rebuild(ctx context.Context, runID string, start time.Time) error {
	if err := c.ops.InitRun(ctx, runID, start, c.cfg.HorizonDays); err != nil {
		return fmt.Errorf("init run: %w", err)
	}

	airports := c.airports
	if airports == nil {
		var err error
		if airports, err = c.ops.Airports(ctx); err != nil {
			return fmt.Errorf("load airports: %w", err)
		}
	}

	instances, err := c.ops.AircraftFlights(ctx, runID, c.cfg.AircraftID)
	if err != nil {
		return fmt.Errorf("load schedule: %w", err)
	}

	legs := make([]leg, 0, len(instances))
	for _, in := range instances {
		origin, ok1 := airports[in.OriginCode]
		dest, ok2 := airports[in.DestinationCode]
		if !ok1 || !ok2 {
			c.log.Warn("skipping leg with unknown airport",
				"flight_instance", in.ID, "origin", in.OriginCode, "destination", in.DestinationCode)
			continue
		}
		legs = append(legs, leg{
			instanceID:   in.ID,
			flightNumber: in.FlightNumber,
			aircraftID:   in.AircraftID,
			origin:       origin,
			destination:  dest,
			departure:    in.ScheduledDeparture,
			arrival:      in.ScheduledArrival,
		})
	}

	c.mu.Lock()
	c.runID = runID
	c.legs = legs
	c.airports = airports
	c.launched = map[string]bool{}
	c.mu.Unlock()

	c.log.Info("schedule loaded", "run_id", runID, "aircraft_id", c.cfg.AircraftID, "legs", len(legs))
	return nil
}

// drive launches each leg that has become due. A leg already past its arrival is
// marked launched (skipped) so a late-starting dispatcher doesn't fly history.
func (c *Controller) drive(ctx context.Context, now time.Time) {
	c.mu.Lock()
	runID := c.runID
	var due []leg
	for _, l := range c.legs {
		if c.launched[l.instanceID] {
			continue
		}
		if !now.Before(l.arrival) {
			c.launched[l.instanceID] = true // over before we could fly it
			c.log.Info("skipping leg already past arrival",
				"flight_instance", l.instanceID, "flight_number", l.flightNumber)
			continue
		}
		if !now.Before(l.departure) {
			c.launched[l.instanceID] = true
			due = append(due, l)
		}
	}
	c.mu.Unlock()

	for _, l := range due {
		c.launch(ctx, runID, l)
	}
}

func (c *Controller) launch(ctx context.Context, runID string, l leg) {
	planJSON, err := buildPlanJSON(l)
	if err != nil {
		c.log.Error("building plan failed", "flight_instance", l.instanceID, "error", err)
		return
	}
	spec := launcher.Spec{
		Name:       fmt.Sprintf("flight-%s-%s", l.flightNumber, l.instanceID),
		Image:      c.cfg.FlightImage,
		Network:    c.cfg.FlightNetwork,
		AutoRemove: c.cfg.AutoRemove,
		Env: map[string]string{
			"FLIGHT_PLAN_JSON":   planJSON,
			"FLIGHT_RUN_ID":      runID,
			"FLIGHT_INSTANCE_ID": l.instanceID,
			"NATS_URL":           c.cfg.NATSURL,
			"SIM_TICK_SUBJECT":   c.cfg.TickSubject,
			"ADSB_URL":           c.cfg.ADSBURL,
			"OPERATIONS_URL":     c.cfg.OperationsURL,
			"LISTEN_ADDR":        "0.0.0.0:8080",
		},
		Labels: map[string]string{
			"app":             "flight",
			"cna.run_id":      runID,
			"cna.flight":      l.flightNumber,
			"cna.instance_id": l.instanceID,
			"cna.aircraft_id": l.aircraftID,
		},
	}
	id, err := c.launcher.Launch(ctx, spec)
	if err != nil {
		c.log.Error("launch failed", "flight_instance", l.instanceID, "flight_number", l.flightNumber, "error", err)
		return
	}
	c.log.Info("launched flight",
		"flight_instance", l.instanceID, "flight_number", l.flightNumber,
		"route", l.origin.Code+"->"+l.destination.Code, "container", id[:min(12, len(id))])
}

// planFile mirrors the Flight plan JSON (shared scenario shape).
type planFile struct {
	FlightID      string      `json:"flight_id"`
	AircraftID    string      `json:"aircraft_id"`
	Origin        planAirport `json:"origin"`
	Destination   planAirport `json:"destination"`
	DepartureTime string      `json:"departure_time"`
	ArrivalTime   string      `json:"arrival_time"`
}

type planAirport struct {
	IATA         string  `json:"iata"`
	LatitudeDeg  float64 `json:"latitude_deg"`
	LongitudeDeg float64 `json:"longitude_deg"`
}

func buildPlanJSON(l leg) (string, error) {
	pf := planFile{
		FlightID:      l.flightNumber,
		AircraftID:    l.aircraftID,
		Origin:        planAirport{IATA: l.origin.Code, LatitudeDeg: l.origin.Latitude, LongitudeDeg: l.origin.Longitude},
		Destination:   planAirport{IATA: l.destination.Code, LatitudeDeg: l.destination.Latitude, LongitudeDeg: l.destination.Longitude},
		DepartureTime: l.departure.UTC().Format(time.RFC3339),
		ArrivalTime:   l.arrival.UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(pf)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
