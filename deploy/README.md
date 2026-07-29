# Deploy

Local development infrastructure for Pheme.

## Usage
```bash
cp .env.example .env
${EDITOR:-vi} .env  # replace both change-me passwords
docker compose -f docker-compose.yml up -d
```

Services:
- **MongoDB** — `127.0.0.1:27017` (persistence)
- **RabbitMQ** — `127.0.0.1:5672`, management UI at http://127.0.0.1:15672
- **Redis** — `127.0.0.1:6379`

All ports bind to loopback. The Compose file refuses to start until MongoDB and
RabbitMQ credentials are set in `.env`.

Stop and remove:
```bash
docker compose -f docker-compose.yml down        # keep volumes
docker compose -f docker-compose.yml down -v     # wipe data
```

## Kubernetes
The `k8s/` directory is reserved for production manifests (added in the
hardening phase of the roadmap).
