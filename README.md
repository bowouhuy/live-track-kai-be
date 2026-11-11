# live-track-kai — Backend (Go)

Simulasi posisi kereta api Pulau Jawa dari jadwal GAPEKA 2025 (bukan GPS). Pengganti backend Laravel; spesifikasi perilaku ada di `../docs/backend.md`.

- Tanpa database: 3 file JSON mentah dimuat ke memori saat start (~0,4 dtk).
- Realtime lewat **SSE** (`GET /api/stream`), bukan Pusher/Reverb.
- Reload data tanpa restart: `SIGHUP` (swap atomik, data lama tetap dipakai kalau gagal).

## Data

Taruh 3 file ini di `data/raw/` (tidak di-commit):

```
data/raw/gapeka2025_stations.json
data/raw/gapeka2025_trains.json
data/raw/gapeka2025_routes.json
```

Salin dari project Laravel (`data/raw/`), atau unduh baru (jarang-jarang saja):

```bash
docker compose run --rm fetch
```

## Jalankan

```bash
cp .env.example .env
docker compose up -d --build
curl http://127.0.0.1:8080/healthz
```

Setelah ganti file data:

```bash
docker compose kill -s HUP backend
```

Lokal tanpa Docker: `go run ./cmd/server` (baca `data/raw` relatif ke folder ini). Test: `go test ./...`.

## Env

| Variabel | Default | Keterangan |
|---|---|---|
| `BROADCAST_INTERVAL_SECONDS` | `5` | Interval tick SSE. FE membacanya dari `/api/config` |
| `CORS_ALLOWED_ORIGINS` | `*` | Daftar origin dipisah koma, mis. `https://kereta.example.com` |
| `BIND_ADDR` | `127.0.0.1` | Alamat host untuk port yang dipublish. `0.0.0.0` = buka langsung ke IP publik |
| `HOST_PORT` | `8080` | Port di host |
| `PORT`, `DATA_DIR` | `8080`, `/data/raw` | Di dalam container, biasanya tidak perlu diubah |

## API

| Endpoint | Isi |
|---|---|
| `GET /api/stations` | `[{code,name,lat,lon}]` (gzip) |
| `GET /api/rail-lines` | `[[[lat,lon],...],...]` (gzip) |
| `GET /api/trains/live[?status=dalam_perjalanan]` | `{"trains":[...]}` — default semua 1934 jadwal |
| `GET /api/trains/{tr_id}` | Detail + stop + polyline, 404 kalau tidak ada |
| `GET /api/config` | `{"broadcast_interval_seconds":5}` |
| `GET /api/stream` | SSE, event `positions.updated`, data `{"trains":[...]}` (hanya `dalam_perjalanan`). Snapshot terakhir dikirim langsung saat connect |
| `GET /healthz` | Status + jumlah data + jumlah klien SSE |

Bentuk JSON sama dengan versi Laravel. Tambahan di entri live/SSE: `current_stop_seq`, `next_stop_seq`, `seconds_to_next_stop` (supaya FE tidak perlu fetch detail tiap tick).

## Caddy di container (setup VPS)

Backend join network Docker bersama `web` (network stack Caddy), port tidak dibuka ke host.

```bash
docker network create web            # sekali saja, kalau belum ada
```

Di `.env` aktifkan:

```
COMPOSE_FILE=docker-compose.yml:docker-compose.caddy.yml
CADDY_NETWORK=web                    # network eksternal yang dipakai container Caddy
CORS_ALLOWED_ORIGINS=https://kereta.example.com
```

Lalu `docker compose up -d --build` seperti biasa. Container Caddy harus ada di network yang sama (`web`, sudah begitu di compose proxy).

Caddyfile:

```
api.example.com {
    reverse_proxy live-track-kai-backend:8080 {
        flush_interval -1   # wajib untuk SSE
    }
}
```

## Reverse proxy di host (alternatif: nginx)

SSE butuh buffering dimatikan dan timeout panjang:

```nginx
server {
    server_name api.example.com;
    # listen 443 ssl; ssl_certificate ... (certbot)

    gzip on;
    gzip_types application/json;

    location /api/stream {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 1h;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Catatan: kalau FE dibuka lewat HTTPS, backend juga wajib HTTPS (browser memblokir mixed content).

## Struktur

```
cmd/server     HTTP + broadcaster SSE + reload SIGHUP
cmd/fetch      unduh snapshot dari gapeka2025.com
internal/source    struct JSON mentah + loader
internal/dataset   transformasi pipeline → model in-memory, Store atomik
internal/routes    flatten polyline, jarak kumulatif, interpolasi
internal/position  resolveWindow, locateOnTrip, formatClock (docs/backend.md §5)
internal/api       handler HTTP + CORS
internal/realtime  hub SSE + loop broadcast
```
