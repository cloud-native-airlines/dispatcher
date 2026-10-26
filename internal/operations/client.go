// Package operations is a client for the Operations control plane: it initializes
// a run's schedule, reads the aircraft's flight instances, and reads airport
// coordinates needed to build each leg's flight plan.
package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 10 * time.Second}}
}

// FlightInstance mirrors the Operations schedule DTO (snake_case fields).
type FlightInstance struct {
	ID                 string    `json:"id"`
	RunID              string    `json:"run_id"`
	FlightNumber       string    `json:"flight_number"`
	OriginCode         string    `json:"origin_code"`
	DestinationCode    string    `json:"destination_code"`
	AircraftID         string    `json:"aircraft_id"`
	ScheduledDeparture time.Time `json:"scheduled_departure"`
	ScheduledArrival   time.Time `json:"scheduled_arrival"`
	Status             string    `json:"status"`
}

// Airport mirrors the Operations airport DTO.
type Airport struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// InitRun materializes the run's schedule from the given start. An already
// initialized run (409 Conflict) is treated as success so re-launch is safe.
func (c *Client) InitRun(ctx context.Context, runID string, start time.Time, horizonDays int) error {
	body := map[string]any{"start": start.UTC().Format(time.RFC3339)}
	if horizonDays > 0 {
		body["horizon_days"] = horizonDays
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/runs/%s", c.baseURL, runID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer drain(resp)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusConflict:
		return nil // already initialized
	default:
		return fmt.Errorf("init run %s: operations responded %d", runID, resp.StatusCode)
	}
}

// AircraftFlights returns the run's flight instances for one aircraft, ordered by
// scheduled departure.
func (c *Client) AircraftFlights(ctx context.Context, runID, aircraftID string) ([]FlightInstance, error) {
	url := fmt.Sprintf("%s/api/v1/aircraft/%s/flights?run_id=%s", c.baseURL, aircraftID, runID)
	var env struct {
		Flights []FlightInstance `json:"flights"`
	}
	if err := c.getJSON(ctx, url, &env); err != nil {
		return nil, err
	}
	return env.Flights, nil
}

// Airports returns all scenario airports keyed by code.
func (c *Client) Airports(ctx context.Context) (map[string]Airport, error) {
	url := fmt.Sprintf("%s/api/v1/airports", c.baseURL)
	var env struct {
		Airports []Airport `json:"airports"`
	}
	if err := c.getJSON(ctx, url, &env); err != nil {
		return nil, err
	}
	byCode := make(map[string]Airport, len(env.Airports))
	for _, a := range env.Airports {
		byCode[a.Code] = a
	}
	return byCode, nil
}

func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: operations responded %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
