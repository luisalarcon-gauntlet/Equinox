# Deploying Project Equinox to Railway

Railway builds and deploys Equinox using the `Dockerfile`. The `railway.toml` file explicitly configures Docker as the builder.

## Prerequisites

- A [Railway](https://railway.app) account
- GitHub repo connected (e.g. `https://github.com/luisalarcon-gauntlet/Equinox`)

## Step 1: Create a new project

1. Go to [railway.app](https://railway.app) and sign in
2. Click **New Project**
3. Select **Deploy from GitHub repo**
4. Choose your `Equinox` repository
5. Railway will detect the `Dockerfile` and `railway.toml` automatically

## Step 2: Add environment variables

In your Railway project, open the service → **Variables** tab. Add the following:

| Variable | Required | Description |
|----------|----------|-------------|
| `ANTHROPIC_API_KEY` | Yes | Your Anthropic API key for Claude |
| `KALSHI_API_KEY_ID` | Yes | Your Kalshi API key ID |
| `KALSHI_PRIVATE_KEY` | Yes* | Full PEM content of your Kalshi private key (see below) |
| `SERVER_PORT` | No | Overrides port; Railway sets `PORT` automatically |
| `HTTP_TIMEOUT` | No | Default: `10s` |
| `KALSHI_BASE_URL` | No | Default: Kalshi production API |
| `POLYMARKET_BASE_URL` | No | Default: Polymarket Gamma API |

\* On Railway you must use `KALSHI_PRIVATE_KEY` (PEM content) because there is no file mount. For local Docker, use `KALSHI_API_KEY_PATH` instead.

## Step 3: Add the Kalshi private key

Railway cannot mount files, so you must paste the **entire PEM content** into the `KALSHI_PRIVATE_KEY` variable:

1. Open your `kalshi_private_key.pem` file locally
2. Copy the **entire contents** (including `-----BEGIN RSA PRIVATE KEY-----` and `-----END RSA PRIVATE KEY-----`)
3. In Railway → Variables → **New Variable**
4. Name: `KALSHI_PRIVATE_KEY`
5. Value: Paste the full PEM (multi-line is supported)
6. Mark it as **Private** (recommended)

## Step 4: Deploy

1. Railway will build from the Dockerfile and deploy automatically
2. Once deployed, go to **Settings** → **Networking** → **Generate Domain** to get a public URL
3. The app will be available at `https://your-app.railway.app`

## Health check

The `railway.toml` configures a health check at `/health`. Railway will verify the app is running before marking the deployment as healthy.

## Port

Railway sets the `PORT` environment variable. Equinox reads `PORT` when `SERVER_PORT` is not set, so no configuration is needed.

## Troubleshooting

If the app fails to start:

- Check that all required variables are set (`ANTHROPIC_API_KEY`, `KALSHI_API_KEY_ID`, `KALSHI_PRIVATE_KEY`)
- Ensure `KALSHI_PRIVATE_KEY` includes the full PEM header and footer
- View logs in Railway → **Deployments** → select deployment → **View Logs**
