# go_chat — meeting flow & signaling protocol

This is the contract between the Svelte client (`client/`) and the Go server
(`server/`). Media and chat go **peer-to-peer** (WebRTC, full mesh).
The Go server only creates rooms, hands out ICE servers, and relays signaling
messages.

## User flow

1. **Home (`/`)**: the user clicks **Start meeting**, and the client calls `POST /api/rooms`.
   The server creates the room and returns an unguessable id. The client goes to `/room/{id}`.
2. **Invite**: the invite link is just `https://<host>/room/{id}`. The link *is*
   the credential, so the id must be random enough that nobody can guess it (see Security).
3. **Lobby (`/room/{id}`)**: the client calls `GET /api/rooms/{id}` to check that the room
   exists, shows a camera/mic preview, asks for a display name, and lets the user mute
   or turn off the camera *before* joining. The **Copy invite link** button is here too.
4. **Join**: the client opens `WS /ws/{id}` and sends `join`. The server replies with
   `welcome` (your id and the peers already in the room) and sends `peer-joined` to everyone else.
5. **Connect**: **the newcomer sends the offers** to every existing peer. Existing
   peers only answer. Because only one side starts, the two sides never send offers at the
   same time (glare). The client still uses "perfect negotiation" as a safety net for ICE restarts.
6. **In call**: audio/video go over RTP. Chat messages and mic/cam state go over a
   negotiated RTCDataChannel (`id: 0`). None of this touches the server.
7. **Leave**: the client closes the WebSocket. The server sends `peer-left` to the room
   and deletes the room once it has been empty for some time (e.g. 10 min).

Changes from a naive "host starts, others join" design:
- **The server generates room ids**, not the client, so it can enforce randomness and limits.
- **The lobby comes before connecting**. Device permission prompts and name entry happen
  before any signaling, so peers never see half-joined ghosts.
- **Mesh limits.** Each peer uploads its stream N-1 times, so the call works well for 2–4 people
  and is usable up to about 6. Cap rooms at 6 (`error: "room full"`). For bigger
  meetings, switch to an SFU (e.g. Pion's `sfu-ws` example); the client changes very little.
- **TURN is required in production.** About 10–20% of real users sit behind symmetric NAT and
  cannot connect with STUN only. Run coturn and give out short-lived credentials from
  `GET /api/ice` (never hard-code TURN secrets in the client).
- **HTTPS is required** in production. `getUserMedia` only works on secure origins
  (localhost is exempt).

## HTTP API

| Method & path         | Response                                            |
|-----------------------|-----------------------------------------------------|
| `POST /api/rooms`     | `201 {"id":"<roomId>"}`                             |
| `GET /api/rooms/{id}` | `200 {"id":"…","peers":2}` or `404`                 |
| `GET /api/ice`        | `200 {"iceServers":[RTCIceServer, …]}`              |

The client accepts room ids matching `^[A-Za-z0-9_-]{8,64}$`.
Suggested format: 16 bytes from `crypto/rand`, encoded with `base64.RawURLEncoding` (22 chars).

`/api/ice` example with coturn's `use-auth-secret` (TURN REST API):
```json
{"iceServers":[
  {"urls":"stun:turn.example.com:3478"},
  {"urls":["turn:turn.example.com:3478?transport=udp","turns:turn.example.com:5349"],
   "username":"<expiryUnix>:<random>","credential":"<base64(hmac-sha1(secret, username))>"}
]}
```
If this endpoint fails, the client falls back to `stun:stun.l.google.com:19302`.

## WebSocket `WS /ws/{roomId}`

All frames are JSON text. Every message has a `type`.

### Client → server

```jsonc
{"type":"join","name":"Alice"}                       // first message, exactly once
{"type":"signal","to":"<peerId>","data":{...}}       // relay to one peer in the same room
```

### Server → client

```jsonc
{"type":"welcome","id":"<yourId>","peers":[{"id":"p1","name":"Bob"}]}
{"type":"peer-joined","peer":{"id":"p2","name":"Carol"}}
{"type":"peer-left","id":"p2"}
{"type":"signal","from":"<peerId>","data":{...}}     // server sets "from", never the client
{"type":"error","message":"room not found" | "room full" | "..."}
```

`data` is opaque to the server. The client sends one of:
```jsonc
{"description":{"type":"offer"|"answer","sdp":"..."}}
{"candidate":{"candidate":"...","sdpMid":"0","sdpMLineIndex":0}}
```

### Ordering the server must guarantee
- Send `peer-joined` to the existing peers **before** sending `welcome` to the newcomer,
  so a peer never receives an offer from someone it hasn't heard of yet.
- Relay signals for one peer pair in order (one writer goroutine per connection does this).

## Security checklist for the Go server
- **Room ids:** at least 128 bits from `crypto/rand`. Never use sequential ids or `math/rand`.
- **Check `Origin`** on the WebSocket upgrade against an allowlist (to block cross-site WebSocket hijacking).
  Note that `gorilla/websocket` and `coder/websocket` allow only same-origin by default; don't disable that.
- **Set `from` on the server.** Relay a `signal` only if `to` is in the *same room*.
- **Limits:** max message size (e.g. 64 KB, since SDP can be ~10 KB), name length ≤ 40 runes,
  peers per room ≤ 6, rooms per IP, plus a read deadline and ping/pong to drop dead sockets.
- **Reject** messages before `join` and a second `join`.
- Don't log SDP or candidates in production, because they contain users' IP addresses.

## Dev setup
Vite proxies `/api` and `/ws` to `http://localhost:8080` (override with
`API_TARGET=http://host:port npm run dev`). Run the Go server on :8080 and open
http://localhost:5173. To test multiple peers locally, use two browser windows
(or one normal window and one incognito window).
