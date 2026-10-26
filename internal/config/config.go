// Package config loads the dispatcher's runtime settings from the environment.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config is the resolved startup configuration.
type Config struct {
	// Upstreams the dispatcher talks to.
	NATSURL       string // subscribe to Simulator ticks (tracks sim time + run)
	TickSubject   string // NATS subject for ticks
	SimURL        string // GET /clock for the initial run + simulated time
	OperationsURL string // init the run, read the schedule

	// Passed through to each launched Flight so it can report home.
	ADSBURL string

	// Which aircraft's schedule this dispatcher flies.
	AircraftID string

	// Launch settings.
	FlightImage   string        // image to run per leg
	FlightNetwork string        // docker network to attach legs to (so names resolve)
	AutoRemove    bool          // remove a leg's container when it exits
	HorizonDays   int           // schedule horizon to request (0 = let Operations default)
	PollInterval  time.Duration // fallback poll of /clock to catch run changes while paused

	ListenAddr string // the dispatcher's own health endpoint
}

// Load resolves configuration from the environment, applying compose-friendly
// defaults so a bare `docker run` with the socket mounted works.
func Load() Config {
	return Config{
		NATSURL:       envString("NATS_URL", ""),
		TickSubject:   envString("SIM_TICK_SUBJECT", "cna.sim.tick"),
		SimURL:        envString("SIM_URL", "http://simulator:8000"),
		OperationsURL: envString("OPERATIONS_URL", "http://operations:8080"),
		ADSBURL:       envString("ADSB_URL", "http://adsb:8080"),
		AircraftID:    envString("AIRCRAFT_ID", "N100CA"),
		FlightImage:   envString("FLIGHT_IMAGE", "cloud-native-airlines/flight:local"),
		FlightNetwork: envString("FLIGHT_NETWORK", ""),
		AutoRemove:    envBool("FLIGHT_AUTOREMOVE", false),
		HorizonDays:   envInt("HORIZON_DAYS", 0),
		PollInterval:  time.Duration(envInt("CLOCK_POLL_SECONDS", 10)) * time.Second,
		ListenAddr:    envString("LISTEN_ADDR", "0.0.0.0:8080"),
	}
}

func envString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envBool(name string, def bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
