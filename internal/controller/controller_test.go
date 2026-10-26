package controller

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cloud-native-airlines/dispatcher/internal/config"
	"github.com/cloud-native-airlines/dispatcher/internal/launcher"
	"github.com/cloud-native-airlines/dispatcher/internal/operations"
)

type fakeLauncher struct {
	mu    sync.Mutex
	specs []launcher.Spec
}

func (f *fakeLauncher) Launch(_ context.Context, s launcher.Spec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, s)
	return "containerid123456", nil
}

func (f *fakeLauncher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.specs)
}

func testController(l legLauncher) *Controller {
	c := New(config.Config{AircraftID: "N100CA", FlightImage: "img"}, nil, l,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.runID = "run-1"
	return c
}

func testLegs() []leg {
	rst := operations.Airport{Code: "RST", Latitude: 43.9083, Longitude: -92.5}
	msp := operations.Airport{Code: "MSP", Latitude: 44.8848, Longitude: -93.2223}
	sfo := operations.Airport{Code: "SFO", Latitude: 37.6213, Longitude: -122.379}
	return []leg{
		{instanceID: "run-1-0000", flightNumber: "CNA1000", aircraftID: "N100CA", origin: rst, destination: msp,
			departure: tm("12:00"), arrival: tm("12:40")},
		{instanceID: "run-1-0001", flightNumber: "CNA0101", aircraftID: "N100CA", origin: msp, destination: sfo,
			departure: tm("13:40"), arrival: tm("17:00")},
	}
}

func tm(hhmm string) time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-01-01T"+hhmm+":00Z")
	return t
}

func TestDrive_LaunchesWhenDueAndOnlyOnce(t *testing.T) {
	f := &fakeLauncher{}
	c := testController(f)
	c.legs = testLegs()

	c.drive(context.Background(), tm("11:00")) // before any departure
	if f.count() != 0 {
		t.Fatalf("expected no launches before first departure, got %d", f.count())
	}

	c.drive(context.Background(), tm("12:10")) // leg 1 is airborne
	if f.count() != 1 {
		t.Fatalf("expected leg 1 launched, got %d", f.count())
	}
	c.drive(context.Background(), tm("12:20")) // still leg 1's window
	if f.count() != 1 {
		t.Fatalf("expected no re-launch of leg 1, got %d", f.count())
	}

	c.drive(context.Background(), tm("14:00")) // leg 2 is airborne
	if f.count() != 2 {
		t.Fatalf("expected leg 2 launched, got %d", f.count())
	}

	spec := f.specs[0]
	if spec.Env["FLIGHT_INSTANCE_ID"] != "run-1-0000" || spec.Env["FLIGHT_RUN_ID"] != "run-1" {
		t.Errorf("leg 1 env wrong: %+v", spec.Env)
	}
	if spec.Labels["cna.flight"] != "CNA1000" {
		t.Errorf("leg 1 labels wrong: %+v", spec.Labels)
	}
}

func TestDrive_SkipsLegsAlreadyPastArrival(t *testing.T) {
	f := &fakeLauncher{}
	c := testController(f)
	c.legs = testLegs()

	// Dispatcher wakes up after both legs are over.
	c.drive(context.Background(), tm("18:00"))
	if f.count() != 0 {
		t.Fatalf("expected no launches for past legs, got %d", f.count())
	}
}

func TestBuildPlanJSON(t *testing.T) {
	l := testLegs()[0]
	raw, err := buildPlanJSON(l)
	if err != nil {
		t.Fatalf("buildPlanJSON: %v", err)
	}
	var pf planFile
	if err := json.Unmarshal([]byte(raw), &pf); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pf.FlightID != "CNA1000" || pf.AircraftID != "N100CA" {
		t.Errorf("identity wrong: %+v", pf)
	}
	if pf.Origin.IATA != "RST" || pf.Origin.LatitudeDeg != 43.9083 {
		t.Errorf("origin wrong: %+v", pf.Origin)
	}
	if pf.Destination.IATA != "MSP" || pf.Destination.LongitudeDeg != -93.2223 {
		t.Errorf("destination wrong: %+v", pf.Destination)
	}
	if pf.DepartureTime != "2026-01-01T12:00:00Z" || pf.ArrivalTime != "2026-01-01T12:40:00Z" {
		t.Errorf("times wrong: dep=%s arr=%s", pf.DepartureTime, pf.ArrivalTime)
	}
}
