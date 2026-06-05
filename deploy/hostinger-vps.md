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
export BACKROUTE_GEOIP_ENABLED=true
export BACKROUTE_PORT_START=2222
export BACKROUTE_PORT_END=2999
docker compose up -d --build
```

The Docker Compose file uses `network_mode: host` on the Ubuntu VPS. This lets BackRoute open new SSH ports dynamically when you create nodes from the portal, without adding every port to `docker-compose.yml`.

## Create Nodes From The Portal

1. Open `http://YOUR_VPS_IP:8080/dashboard/`.
2. Log in with `BACKROUTE_DASHBOARD_USER` and `BACKROUTE_DASHBOARD_PASSWORD`.
3. Type a node name such as `node-1`.
4. Keep the target as `127.0.0.1:22` for SSH.
5. Click `Create node`.
6. Copy the agent command shown in the portal to the Ubuntu machine behind the firewall.
7. Use the SSH command shown in the portal from your laptop.

Open the VPS firewall for the portal and the dynamic SSH range:

```bash
sudo ufw allow 8080/tcp
sudo ufw allow 2222:2999/tcp
```

## Next Production Steps

- Add a domain such as `api.backroute.i2itelesource.com`.
- Put Caddy or Nginx in front for HTTPS.
- Store tokens in a database instead of environment variables.
- Add firewall rules to expose only ports 80, 443, and required tunnel ports.
