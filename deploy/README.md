# Deploy

Local development infrastructure for Pheme.

## Usage
```bash
cp .env.example .env
docker compose -f docker-compose.yml up -d
```

Services:
- **MongoDB** — `localhost:27017` (persistence)
- **RabbitMQ** — `localhost:5672`, management UI at http://localhost:15672
- **Redis** — `localhost:6379`

Stop and remove:
```bash
docker compose -f docker-compose.yml down        # keep volumes
docker compose -f docker-compose.yml down -v     # wipe data
```

## Kubernetes
The `k8s/` directory is reserved for production manifests (added in the
hardening phase of the roadmap).
