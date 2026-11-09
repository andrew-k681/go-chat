# go-chat

Peer-to-peer video meetings. Audio, video and chat go directly between browsers
over WebRTC (full mesh, up to 6 people). The Go server only creates rooms, hands
out ICE servers and relays signaling messages. Optionally, it publishes room
events to Kafka, and a small stats service turns them into numbers.

```
browser ◄──WebRTC (media + chat)──► browser
   │                                   │
   └──── HTTP + WebSocket signaling ───┤
                                       ▼
                          server (Go) ──protobuf──► Kafka "room-events" ──► stats (Go)
                                                                             GET /stats
```

The client/server contract is described in [PROTOCOL.md](PROTOCOL.md).

## Prerequisites

| Tool | Needed for |
|---|---|
| Go 1.27+ | server, stats, code generation |
| Node.js 22+ and npm | client |
| Docker (with Compose) | Kafka (optional) |
| `make` | shortcuts below (optional) |

You don't need to install `buf` or `protoc-gen-go` yourself: they run through
`go run` / `go tool` at pinned versions.

## 1. Run the app (no Kafka)

Use two terminals:

```sh
# terminal 1: signaling server on :8080
cd server
go run .
```

```sh
# terminal 2: client on :5173 (Vite proxies /api and /ws to :8080)
cd client
npm install
npm run dev
```

Open http://localhost:5173, click **Start meeting**, and open the invite link in a
second window (or an incognito window) to join as another person.

## 2. Run with Kafka and stats

Start Kafka (Apache Kafka 4.x in KRaft mode, plus a web UI):

```sh
make kafka-up        # first start takes ~30 s; the topic is created automatically
docker compose ps -a # kafka should be "healthy"; init-topics "Exited (0)"
```

Create the secrets once. The programs read the real environment and don't load
`.env` files, so export them in your shell:

```sh
export EVENTS_KEY=$(openssl rand -base64 32)   # HMAC key for room_ref
export STATS_TOKEN=$(openssl rand -base64 32)  # bearer token for /stats
export KAFKA_BROKERS=localhost:19092
```

Then use three terminals (each needs the exports above):

```sh
# terminal 1: signaling server, now publishing events
cd server && go run .          # log line shows kafka=true
```

```sh
# terminal 2: stats service on 127.0.0.1:8081
cd server && go run ./cmd/stats
```

```sh
# terminal 3: client
cd client && npm run dev
```

Join a call in two windows, leave, and then:

```sh
curl -s -H "Authorization: Bearer $STATS_TOKEN" http://127.0.0.1:8081/stats
```

```json
{"rooms_created":1,"rooms_expired":0,"rooms_live":1,"joins":2,"rejected_room_full":0,
 "peak_peers_in_room":2,"sessions_completed":2,"avg_session_seconds":41.5,
 "open_sessions":0,"rooms_per_day":{"2026-10-26":1},"unknown_events":0,
 "decode_errors":0,"duplicates":0}
```

To look inside Kafka:

| Command | Shows |
|---|---|
| http://localhost:8088 | Kafka UI: topics, partitions, messages, consumer groups |
| `make kafka-topics` | partitions, leader and in-sync replicas of `room-events` |
| `make kafka-groups` | consumer groups and their lag (stats commits no offsets: it replays the topic on every start) |
| `make kafka-down` | stops Kafka; data is kept in the `kafka-data` volume |
| `docker compose down -v` | stops Kafka **and deletes all events** |

Message values are binary protobuf, so the UI shows them as bytes. The schema is
in [proto/gochat/events/v1/events.proto](proto/gochat/events/v1/events.proto).

## Development

```sh
make test       # go test -race ./... in server/
make generate   # after editing a .proto: regenerates server/gen/
make lint       # buf lint on the protobuf schema
make breaking   # fails if the schema changed incompatibly compared to main
```

Code generated into `server/gen/` is committed. Don't edit it by hand.

Client checks: `cd client && npm run check && npm run lint`.

## Configuration

All settings come from environment variables. [.env.example](.env.example) lists them
all; copy it to `.env` (git-ignored) and load it with `set -a; source .env; set +a`.
Never commit real secrets.

**Signaling server** (`server/`)

| Variable | Default | Notes |
|---|---|---|
| `ADDR` | `:8080` | listen address |
| `ALLOW_ORIGINS` | `http://localhost:5173` | exact origins allowed to open the WebSocket; `*` is rejected |
| `MAX_PEERS` | `6` | peers per room (2–16) |
| `ROOM_TTL` | `10m` | how long an empty room is kept |
| `STUN_URLS` | Google STUN | comma-separated |
| `TURN_URLS`, `TURN_SECRET` | — | coturn `use-auth-secret`; secret ≥ 32 chars. Required in production |
| `TURN_TTL` | `1h` | lifetime of handed-out TURN credentials |
| `STATIC_DIR` | — | serve a built client from this directory |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `KAFKA_BROKERS` | — | `host:port,…`; empty = no events |
| `KAFKA_TOPIC` | `room-events` | |
| `EVENTS_KEY` | — | ≥ 32 chars, required with Kafka |
| `KAFKA_TLS` | `false` | |
| `KAFKA_SASL_USER`, `KAFKA_SASL_PASSWORD`, `KAFKA_SASL_MECHANISM` | — | SCRAM-SHA-256 (default) or SCRAM-SHA-512 |

**Stats service** (`server/cmd/stats`): uses the same `KAFKA_*` variables, plus:

| Variable | Default | Notes |
|---|---|---|
| `STATS_ADDR` | `127.0.0.1:8081` | loopback by default; put a TLS proxy in front to expose it |
| `STATS_TOKEN` | — | required, ≥ 32 chars |
| `KAFKA_GROUP` | `stats` | consumer group id |

## Project layout

```
PROTOCOL.md               client ↔ server contract, room events
proto/                    protobuf schema (buf module)
docker-compose.yml        local Kafka + Kafka UI
client/                   SvelteKit app
server/
  main.go                 signaling server
  cmd/stats/              stats service (Kafka consumer)
  gen/                    generated protobuf code
  internal/config/        env var parsing and validation
  internal/hub/           rooms, peers, signaling relay, lifecycle events
  internal/server/        HTTP API, WebSocket, static files
  internal/ice/           STUN/TURN servers and TURN credentials
  internal/events/        protobuf encoding + Kafka producer
  internal/stats/         Kafka consumer, aggregation, GET /stats
```

## Troubleshooting

- **The server exits with `config: …`**: every invalid variable is listed. Fix them and restart.
- **`kafka` never becomes healthy**: check `docker compose logs kafka`. On a fresh machine, the
  first start needs to pull the image and boot the JVM.
- **Stats stay at 0**: check that both processes use the same `KAFKA_BROKERS` and
  `KAFKA_TOPIC`, and that the server log says `kafka=true`. The server log line
  `room event dropped` means Kafka wasn't reachable.
- **Camera doesn't work from another device**: `getUserMedia` needs HTTPS (only localhost is exempt).
- **Peers can't see each other across networks**: configure TURN. STUN alone fails behind
  symmetric NAT.
