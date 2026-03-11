# Deploying Project Equinox to Railway

Railway builds and deploys Equinox using the `Dockerfile`. The `railway.toml`
file explicitly configures Docker as the builder.

## Prerequisites

1. A [Railway](https://railway.app) account
2. A connected GitHub repo, such as `https://github.com/luisalarcon-gauntlet/Equinox`

## Step 1: Create a new project

1. Go to [railway.app](https://railway.app) and sign in.
2. Click **New Project**.
3. Select **Deploy from GitHub repo**.
4. Choose your `Equinox` repository.
5. Railway will detect the `Dockerfile` and `railway.toml` automatically.

## Step 2: Add environment variables

In your Railway project, open the service's **Variables** tab and add:

| Variable | Required | Description |
|----------|----------|-------------|
| `OPENAI_API_KEY` | Yes | Your OpenAI API key for `gpt-4.1-nano` |
| `OPENAI_BASE_URL` | No | Override the OpenAI API base URL if needed |
| `SERVER_PORT` | No | Overrides port; Railway sets `PORT` automatically |
| `HTTP_TIMEOUT` | No | Default: `10s` |
| `KALSHI_BASE_URL` | No | Default: `https://api.elections.kalshi.com` |
| `POLYMARKET_BASE_URL` | No | Default: Polymarket Gamma API |

No Kalshi API key or private key is required for read-only market search.

## Step 3: Deploy

1. Railway will build from the `Dockerfile` and deploy automatically.
2. Once deployed, go to **Settings** -> **Networking** -> **Generate Domain** to get a public URL.
3. The app will be available at `https://your-app.railway.app`.

## Health Check

The `railway.toml` configures a health check at `/health`. Railway will verify
the app is running before marking the deployment as healthy.

## Port

Railway sets the `PORT` environment variable. Equinox reads `PORT` when
`SERVER_PORT` is not set, so no extra configuration is needed.

## Troubleshooting

If the app fails to start:

1. Check that `OPENAI_API_KEY` is set.
2. If you override `KALSHI_BASE_URL`, make sure it points at the Kalshi v1 host root.
3. View logs in Railway -> **Deployments** -> select the deployment -> **View Logs**.
