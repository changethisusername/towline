# n8n Operations

This stack runs n8n, an open-source workflow automation tool, backed by Postgres.

## Key environment variables

- N8N_ENCRYPTION_KEY: Required for credential encryption. Set this before first deploy.
- DB_PASSWORD: Postgres database password.
- DB_NAME: Database name (default: n8n).
- DB_USER: Database user (default: n8n).

## Access

n8n web UI runs on port 5678. After deploying, access it at http://your-host:5678.
On first access, you'll create an admin account through the n8n setup wizard.

## Common tasks

### Check n8n logs for workflow errors
Use towline_service_logs with service "n8n" and tail 200. Filter for "error"
to find workflow execution failures.

### Backup n8n data
n8n stores workflows in Postgres and credentials encrypted in the n8n_data
volume. Both must be backed up together. The encryption key is critical —
without it, credentials cannot be decrypted.

## Troubleshooting

### n8n can't connect to database
Check that the db service is running with towline_service_health. Verify
DB_PASSWORD is set correctly with towline_env_get.

### Workflows not triggering
Check n8n logs for webhook registration errors. If using an external trigger
URL, ensure the domain is configured via towline_domains_add.
