# Hostinger VPS Deployment Notes

Target operating system: Ubuntu 22.04 or 24.04 LTS.

## Basic Server Setup

```bash
sudo apt update
sudo apt install -y git docker.io docker-compose-plugin
sudo systemctl enable --now docker
```

## Deploy BackRoute

```bash
git clone https://github.com/backrouteio/backroute.git
cd backroute
export BACKROUTE_TOKEN="replace-with-a-strong-token"
export BACKROUTE_DASHBOARD_USER="br_admin_ops_2026"
export BACKROUTE_DASHBOARD_PASSWORD="replace-with-a-strong-dashboard-password"
docker compose up -d --build
```

## Next Production Steps

- Add a domain such as `api.backroute.i2itelesource.com`.
- Put Caddy or Nginx in front for HTTPS.
- Store tokens in a database instead of environment variables.
- Add firewall rules to expose only ports 80, 443, and required tunnel ports.
