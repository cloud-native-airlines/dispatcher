# Dispatcher

The dispatcher flies a Cloud Native Airlines tour. It is a long-running controller
that keeps a run's schedule in sync with **Operations** and launches **one
ephemeral Flight container per leg** as simulated time reaches each leg's
scheduled departure — the Docker Compose stand-in for "a pod (or Kubernetes Job)
per leg", which is the point of the demo: lots of short-lived jobs starting and
stopping against a durable control plane.

## What it does

1. On startup (and on every Simulator run reset), reads the Simulator clock,
   initializes the run's schedule in Operations (`POST /api/v1/runs/{runId}`), and
   loads the aircraft's flight instances plus airport coordinates.
2. Tracks simulated time via NATS ticks (with a fallback `/clock` poll).
3. When sim time reaches a leg's scheduled departure, launches a Flight container
   for that leg via the Docker API, passing the per-leg plan inline
   (`FLIGHT_PLAN_JSON`) plus the run/instance ids and the ADS-B/Operations URLs.
   The Flight flies the leg, reports positions to ADS-B and departure/arrival
   events to Operations, then exits on landing.

Because the schedule already spaces legs by block + turnaround, launching each at
its departure naturally sequences the tour (RST→MSP→SFO→ATL→RST).

## Configuration (environment)

| Var | Default | Purpose |
| --- | --- | --- |
| `NATS_URL` | — | subscribe to Simulator ticks (empty = poll only) |
| `SIM_TICK_SUBJECT` | `cna.sim.tick` | tick subject |
| `SIM_URL` | `http://simulator:8000` | clock fetch |
| `OPERATIONS_URL` | `http://operations:8080` | init run + read schedule |
| `ADSB_URL` | `http://adsb:8080` | passed to launched Flights |
| `AIRCRAFT_ID` | `N100CA` | whose schedule to fly |
| `FLIGHT_IMAGE` | `cloud-native-airlines/flight:local` | image to launch per leg |
| `FLIGHT_NETWORK` | — | docker network to attach legs to (so names resolve) |
| `FLIGHT_AUTOREMOVE` | `false` | remove a leg's container when it exits |
| `HORIZON_DAYS` | `0` | schedule horizon to request (0 = Operations default) |
| `CLOCK_POLL_SECONDS` | `10` | fallback `/clock` poll interval |
| `LISTEN_ADDR` | `0.0.0.0:8080` | health endpoint (`/healthz`, `/readyz`) |

The dispatcher needs access to the Docker socket (`/var/run/docker.sock`) to
launch Flight containers.

## Develop

```sh
make test        # go vet + go test
make build-image # build the container image
```
