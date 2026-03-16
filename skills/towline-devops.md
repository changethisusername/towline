# Towline DevOps Skill

You have access to Towline MCP tools for deploying and managing containers on
self-hosted infrastructure via Portainer. This skill teaches you how to use
them effectively.

## Mental model

You are operating on an isolated container stack. You can see and control only
the services within your project's stack. Treat this stack as your entire
infrastructure.

Your stack has a tier: dev or prod. In dev, you have full autonomy. In prod,
destructive and configuration-changing actions require your human partner's
approval. When a prod operation needs approval, the tool will return an
approval token — present the operation details to your partner, and if they
confirm, re-call the tool with the approvalToken parameter.

## Tool inventory

### Observation tools (no side effects, use freely)

- towline_service_health — Health snapshot for one or all services: state,
  uptime, restart count, CPU/memory usage, exit code. Always start here when
  diagnosing issues.
  - Parameters: service (optional string)

- towline_service_logs — Recent log output for a service. Strips Docker
  binary framing.
  - Parameters: service (required), tail (int, default 200), since (RFC3339
    timestamp, optional), filter (string, optional grep-like line filter)

- towline_env_get — Read stack environment variables. Variables whose names
  contain KEY, SECRET, PASSWORD, or TOKEN have their values masked.
  - Parameters: name (optional, reads single var)

- towline_domains_list — List all domain/routing mappings for the stack.
  Returns service, domain, port, and proxy method (traefik/caddy/cloudflare).

- towline_deployments — Deployment history with diffs and outcomes.
  - Parameters: limit (int, default 10)

- listLocalStacks — Portainer stack listing (scoped to your stack).

- getLocalStackFile — Current docker-compose.yml content.

- dockerProxy — Low-level Docker API access (scoped to your stack's
  containers). Prefer the higher-level towline tools above.
  - Parameters: environmentId, method, dockerAPIPath, queryParams, headers,
    body

### Action tools (cause changes)

- updateLocalStack — Update the stack's compose definition. Always submit
  the COMPLETE compose file. Include a description of what changed.
  - Parameters: id, endpointId, file (full compose YAML), env (array),
    prune (bool), pullImage (bool)
  - Prod tier: requires approval

- towline_env_set — Update an environment variable. Service restart may be
  needed to pick up the change.
  - Parameters: name (string), value (string)
  - Prod tier: requires approval

- towline_domains_add — Add a domain routing to a service. Supports Traefik
  (labels), Caddy (admin API), or Cloudflare Tunnels (delegates to
  Cloudflare MCP).
  - Parameters: service (string), domain (string), port (int, default 80),
    method (optional: traefik|caddy|cloudflare)
  - Prod tier: requires approval

- towline_domains_remove — Remove a domain routing.
  - Parameters: service (string), domain (string)
  - Prod tier: requires approval

- towline_scale — Scale a service to N replicas. Warns if the service has
  persistent volumes.
  - Parameters: service (string), replicas (int)
  - Prod tier: requires approval

- towline_exec — Run a command inside a running container.
  - Parameters: service (string), command (string or array)
  - Prod tier: requires approval

- startLocalStack / stopLocalStack — Start or stop the entire stack.
  - Prod tier: stop requires approval

- deleteLocalStack — Delete the entire stack.
  - Prod tier: requires approval

## Decision framework

### "Something is broken"

Follow this sequence. Stop as soon as you identify the issue.

1. Start with health. Call towline_service_health (no service parameter) to
   get all services. Look for: containers not running, high restart counts
   (>3 in the last hour = crash loop), exit code 137 (OOM killed), services
   stuck in "restarting" state.

2. Check logs. Call towline_service_logs on the unhealthy service with
   tail: 200. Look for: stack traces, "connection refused" (dependency not
   ready), "permission denied" (volume/entrypoint issue), missing env var
   errors.

3. Check dependency health. If the failing service depends on a database,
   cache, or other service, check that service's health and logs. Most
   "app broken" problems are actually "database not ready."

4. Check environment variables. If logs mention missing config, call
   towline_env_get to verify expected variables are set.

5. Check the compose file. If the issue is structural (wrong image, missing
   volume, port conflict), call getLocalStackFile.

6. Check deployment history. If the service was working recently, call
   towline_deployments. The diff often points directly at the problem.

Do NOT restart without understanding the root cause. A restart fixes transient
issues but masks persistent ones. If a container is crash-looping, restarting
it just adds another crash.

### "Deploy this service"

1. Review current state. Call getLocalStackFile to see the existing compose.
   Understand what's there before modifying.

2. Set environment variables first. If the service needs env vars, call
   towline_env_set before deploying. The deploy picks up current env vars.

3. Deploy. Call updateLocalStack with the COMPLETE compose file. Always
   include a description of what changed.

4. Verify. Call towline_service_health to confirm all services started.
   Then towline_service_logs on the new/updated service.

5. Configure routing if needed. Call towline_domains_add after confirming
   the service is healthy.

### "Expose this service / add a domain"

1. Confirm the service is running with towline_service_health.
2. Check existing domains with towline_domains_list.
3. Add the domain with towline_domains_add. Specify traefik, caddy, or
   cloudflare as the method depending on your proxy setup. For external
   access through Cloudflare Tunnels, use method: "cloudflare" and follow
   the returned Cloudflare MCP instructions.
4. Verify with towline_domains_list.

### "Update a configuration value"

1. Read current value with towline_env_get.
2. Set new value with towline_env_set.
3. Restart the affected service if it requires a restart to pick up env
   changes (most do). Use dockerProxy to restart, or redeploy with
   updateLocalStack.
4. Verify with towline_service_logs.

### "Debug connectivity between services"

1. Check both services are running with towline_service_health.
2. Services in the same stack share a Docker network by default. The
   compose service name is the hostname. Verify: correct service names in
   compose, target service exposes the expected port, no network_mode
   override.
3. Test from inside the container with towline_exec:
   wget -qO- http://service-b:8080/health
   or: nc -zv service-b 5432
4. Check logs on both sides. Connection refusal shows in the client logs;
   the reason shows in the server logs.

### "Roll back a broken deployment"

1. Call towline_deployments to find the last good compose definition.
2. Call updateLocalStack with that compose content. Description:
   "rollback to deployment from [timestamp]"
3. Verify with towline_service_health and towline_service_logs.

## Common mistakes

Partial compose files: Always submit the COMPLETE compose file to
updateLocalStack. If you only include the changed service, Portainer removes
all other services. Always call getLocalStackFile first, modify the result,
and submit the full content.

Restarting without diagnosing: A restart is not a fix. Read logs first.

Setting env vars after deploy: If your compose references env vars that don't
exist yet, the service starts with empty values. Set vars before deploying.

Forgetting dependencies: Before concluding a service is broken, check its
dependencies. A service connecting to postgres on startup crashes immediately
if postgres isn't ready.

Scaling stateful services: Don't scale services with persistent volume mounts
to multiple replicas. You'll get data corruption or mount conflicts.

Ignoring exit codes: 137 = OOM killed (needs more memory). 1 = application
error (check logs). 143 = SIGTERM (graceful shutdown, usually fine).
126 = permission denied on entrypoint. 127 = entrypoint not found.

## Efficiency tips

Batch observations: Call towline_service_health for all services first rather
than inspecting one by one. The overview tells you where to focus.

Use service names: All towline tools accept compose service names. You don't
need to resolve container IDs.

Request enough log lines: Use tail: 200 minimum. A truncated log starting
mid-output is useless for diagnosis.

Describe your deployments: Always include a meaningful description when
deploying. It shows in deployment history and approval requests.

Check before you act: Before any mutation, verify current state. One extra
observation call prevents accidental overwrites.
