package gateway

import (
	"fmt"
	"html/template"
	"time"
)

var pageTemplates = template.Must(template.New("pages").Funcs(template.FuncMap{
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		d := time.Since(t).Round(time.Second)
		switch {
		case d < time.Minute:
			return fmt.Sprintf("%ds ago", int(d.Seconds()))
		case d < time.Hour:
			return fmt.Sprintf("%dm ago", int(d.Minutes()))
		case d < 48*time.Hour:
			return fmt.Sprintf("%dh ago", int(d.Hours()))
		default:
			return fmt.Sprintf("%dd ago", int(d.Hours()/24))
		}
	},
	"until": func(t time.Time) string {
		d := time.Until(t).Round(time.Second)
		if d < 0 {
			return "expired"
		}
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	},
	"short": func(id string) string {
		if len(id) > 8 {
			return id[:8]
		}
		return id
	},
	"kindLabel": func(k string) string {
		switch k {
		case KindToken:
			return "header token"
		case KindOAuth:
			return "client id + secret"
		case KindApp:
			return "app sign-in"
		}
		return k
	},
	"multi": func(n int) bool { return n > 1 },
}).Parse(`
{{define "head"}}<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Towline gateway</title>
<style>
:root { --bg:#f7f7f5; --fg:#1d1d1b; --muted:#6b6b66; --card:#fff; --line:#e3e3de; --ok:#1f7a3d; --no:#b42318; --warn:#9a6700; }
@media (prefers-color-scheme: dark) { :root { --bg:#161615; --fg:#ecece8; --muted:#9a9a93; --card:#20201e; --line:#33332f; --ok:#4cc27a; --no:#f07167; --warn:#e3b341; } }
* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:var(--fg); font:15px/1.5 system-ui, sans-serif; }
main { max-width: 860px; margin: 0 auto; padding: 24px 16px 64px; }
h1 { font-size: 20px; margin: 0 0 4px; } h2 { font-size: 13px; margin: 28px 0 8px; color: var(--muted); text-transform: uppercase; letter-spacing: .04em; }
.card { background:var(--card); border:1px solid var(--line); border-radius:10px; padding:16px; margin:10px 0; overflow-wrap:anywhere; }
.meta { color:var(--muted); font-size:13px; }
code, pre { font: 13px/1.45 ui-monospace, SFMono-Regular, Menlo, monospace; }
pre { background:var(--bg); border:1px solid var(--line); border-radius:6px; padding:10px; overflow:auto; max-height:360px; white-space:pre-wrap; word-break:break-word; }
.secret { font-size:14px; user-select:all; }
.actions { display:flex; gap:8px; margin-top:12px; flex-wrap:wrap; }
button { font:inherit; border-radius:8px; border:1px solid var(--line); background:var(--card); color:var(--fg); padding:8px 16px; cursor:pointer; }
button.primary { background:var(--ok); border-color:var(--ok); color:#fff; } button.danger { color:var(--no); border-color:var(--no); }
input[type=password], input[type=text], textarea, select { font:inherit; width:100%; padding:8px 10px; border-radius:8px; border:1px solid var(--line); background:var(--bg); color:var(--fg); }
label { display:block; margin:10px 0 4px; font-weight:600; }
.choice { font-weight:normal; display:flex; gap:8px; align-items:flex-start; margin:6px 0; }
.choice input { margin-top:4px; }
.msg { padding:10px 12px; border-radius:8px; border:1px solid var(--no); color:var(--no); margin:12px 0; }
.warn { padding:10px 12px; border-radius:8px; border:1px solid var(--warn); color:var(--warn); margin:12px 0; }
.status-approved { color:var(--ok); } .status-rejected, .status-expired { color:var(--no); }
header { display:flex; justify-content:space-between; align-items:center; gap:12px; flex-wrap:wrap; }
table { width:100%; border-collapse:collapse; } td, th { text-align:left; padding:6px 4px; border-top:1px solid var(--line); vertical-align:top; font-size:14px; }
</style>
</head>
<body><main>{{end}}
{{define "foot"}}</main></body></html>{{end}}

{{define "error"}}{{template "head"}}
<h1>Towline gateway</h1>
<div class="msg">{{.Message}}</div>
{{template "foot"}}{{end}}

{{define "login"}}{{template "head"}}
<h1>Towline gateway</h1>
<div class="meta">Owner sign-in</div>
{{with .Message}}<div class="msg">{{.}}</div>{{end}}
<div class="card">
<form method="post" action="/ui/login">
<label for="token">Owner token</label>
<input type="password" id="token" name="token" autocomplete="current-password" autofocus>
<div class="actions"><button class="primary">Sign in</button></div>
</form>
</div>
{{template "foot"}}{{end}}

{{define "dashboard"}}{{template "head"}}
<header><div><h1>Towline gateway</h1><div class="meta">{{.PublicURL}}</div></div>
<form method="post" action="/ui/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Sign out</button></form>
</header>
{{with .Message}}<div class="msg">{{.}}</div>{{end}}

<h2>Waiting for your approval ({{len .Pending}})</h2>
{{range .Pending}}
<div class="card">
<div><strong>{{.Action}}</strong> on <strong>{{.Project}}</strong>{{with .Client}} <span class="meta">by connection {{.}}</span>{{end}}</div>
<div class="meta">Requested {{ago .CreatedAt}} · id <code>{{short .ID}}</code></div>
{{with .Description}}<pre>{{.}}</pre>{{end}}
{{with .StackContent}}<details><summary>Compose file</summary><pre>{{.}}</pre></details>{{end}}
<form method="post" action="/ui/approvals" class="actions">
<input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="id" value="{{.ID}}">
<button class="primary" name="decision" value="approve">Approve</button>
<button class="danger" name="decision" value="reject">Reject</button>
</form>
</div>
{{else}}<div class="card meta">Nothing is waiting.</div>{{end}}

<h2>Connect an app</h2>
<div class="card">
<form method="post" action="/ui/connections">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<label for="name">Name</label>
<input type="text" id="name" name="name" maxlength="64" placeholder="e.g. Claude on my phone" required>
<label>Projects</label>
{{range .Projects}}<div class="choice"><input type="checkbox" name="project" value="{{.Name}}" id="p-{{.Name}}"><label class="choice" for="p-{{.Name}}">{{.Name}} <span class="meta">{{.Tier}} · approval {{.Approval}}</span></label></div>{{end}}
<label>Access</label>
<div class="choice"><input type="radio" name="scope" value="read" id="s-read" checked><label class="choice" for="s-read">Read only: health, logs, env, stack file</label></div>
<div class="choice"><input type="radio" name="scope" value="deploy" id="s-deploy"><label class="choice" for="s-deploy">Deploy: also change stacks, env, domains, exec (gated changes wait for your approval here)</label></div>
<div class="warn">An app that can deploy and reads untrusted content (web pages, logs, email) can be tricked into deploying things. Give deploy access to as few projects as you can, and read approval requests carefully.</div>
<label>How the app connects</label>
<div class="choice"><input type="radio" name="method" value="app" id="m-app" checked><label class="choice" for="m-app">App sign-in: Claude, ChatGPT/Codex and other apps that add a connector by URL. Opens a 10-minute window; you approve the app on this gateway when it signs in.</label></div>
<div class="choice"><input type="radio" name="method" value="oauth" id="m-oauth"><label class="choice" for="m-oauth">Client ID and secret: for apps where you can enter an OAuth client ID and secret (Claude custom connector, advanced settings).</label></div>
<div class="choice"><input type="radio" name="method" value="token" id="m-token"><label class="choice" for="m-token">Header token: for clients that send an Authorization header (Claude Code, Cursor, scripts).</label></div>
<details><summary class="meta">Extra redirect URIs (client ID and secret only)</summary>
<textarea name="redirect_uris" rows="2" placeholder="One per line. Claude's callbacks are always included."></textarea>
</details>
<div class="actions"><button class="primary">Create</button></div>
</form>
</div>

{{if .Pairings}}<h2>Open sign-in windows</h2>
{{range .Pairings}}<div class="card"><strong>{{.Name}}</strong> <span class="meta">· {{range $i, $p := .Projects}}{{if $i}}, {{end}}{{$p}}{{end}} · {{.Scope}} · closes in {{until .ExpiresAt}}</span>
<form method="post" action="/ui/pairings/cancel" class="actions"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><button class="danger">Cancel</button></form></div>{{end}}
{{end}}

<h2>Connections ({{len .Connections}})</h2>
{{range .Connections}}
<div class="card">
<div><strong>{{.Name}}</strong> <span class="meta">· {{kindLabel .Kind}}{{with .ClientName}} · app says it is "{{.}}"{{end}}</span></div>
<div class="meta">{{.Scope}} on {{range $i, $p := .Projects}}{{if $i}}, {{end}}{{$p}}{{end}} · created {{ago .CreatedAt}} · last used {{ago .LastUsedAt}}</div>
{{if and (eq .Scope "deploy") (multi (len .Projects))}}<div class="warn">Deploy access to several projects: content from one project can influence changes to another.</div>{{end}}
<form method="post" action="/ui/connections/revoke" class="actions"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><button class="danger">Revoke</button></form>
</div>
{{else}}<div class="card meta">No connections yet.</div>{{end}}

<h2>Projects</h2>
<div class="card"><table>
<tr><th>Project</th><th>Stack</th><th>Tier</th><th>Approval</th></tr>
{{range .Projects}}<tr><td>{{.Name}}</td><td><code>{{.Stack}}</code></td><td>{{.Tier}}</td><td>{{if eq .Approval "always"}}all changes{{else}}prod changes{{end}}</td></tr>{{end}}
</table>
<div class="meta">Projects are added with <code>towline remote deploy</code> on your machine.</div></div>

{{if .Recent}}<h2>Recent decisions</h2>
{{range .Recent}}<div class="card"><strong>{{.Action}}</strong> on {{.Project}} · <span class="status-{{.Status}}">{{.Status}}</span> <span class="meta">· {{ago .CreatedAt}} · <code>{{short .ID}}</code></span></div>{{end}}
{{end}}
{{template "foot"}}{{end}}

{{define "created"}}{{template "head"}}
<h1>Connection created</h1>
<div class="warn">Copy the secret now. It is shown only once; the gateway stores only its hash.</div>
<div class="card">
<div><strong>{{.Conn.Name}}</strong> <span class="meta">· {{.Conn.Scope}} on {{range $i, $p := .Conn.Projects}}{{if $i}}, {{end}}{{$p}}{{end}}</span></div>
<label>Server URL</label>
{{range .URLs}}<div class="meta">{{.Label}}</div><pre class="secret">{{.URL}}</pre>{{end}}
{{if eq .Conn.Kind "oauth"}}
<label>OAuth client ID</label><pre class="secret">{{.Conn.ClientID}}</pre>
<label>OAuth client secret</label><pre class="secret">{{.Conn.Secret}}</pre>
<p class="meta">In Claude: Settings → Connectors → Add custom connector. Paste the server URL, open Advanced settings and paste the client ID and secret. The secret only works for signing in, not as a bearer token.</p>
{{else}}
<label>Bearer token</label><pre class="secret">{{.Conn.Secret}}</pre>
<p class="meta">Claude Code:</p>
<pre>claude mcp add --transport http towline {{(index .URLs 0).URL}} --header "Authorization: Bearer {{.Conn.Secret}}"</pre>
<p class="meta">Other clients: send <code>Authorization: Bearer &lt;token&gt;</code> to the server URL.</p>
{{end}}
</div>
<div class="actions"><a href="/ui">Back to the gateway</a></div>
{{template "foot"}}{{end}}

{{define "paired"}}{{template "head"}}
<h1>Sign-in window open</h1>
<div class="card">
<p>Within <strong>10 minutes</strong>, add this server as a connector in your app:</p>
{{range .URLs}}<div class="meta">{{.Label}}</div><pre class="secret">{{.URL}}</pre>{{end}}
<p class="meta">The app will open a sign-in page on this gateway. Check that it is the app you just added, then approve it. It gets <strong>{{.Pairing.Scope}}</strong> access to {{range $i, $p := .Pairing.Projects}}{{if $i}}, {{end}}{{$p}}{{end}}.</p>
</div>
<div class="actions"><a href="/ui">Back to the gateway</a></div>
{{template "foot"}}{{end}}

{{define "consent"}}{{template "head"}}
<h1>Allow an app to use Towline?</h1>
{{with .Message}}<div class="msg">{{.}}</div>{{end}}
<div class="card">
<p>An app that calls itself <strong>"{{.ClientName}}"</strong> wants access. After you approve, the browser goes back to <strong><code>{{.Redirect}}</code></strong>.</p>
<p class="meta">Only continue if you started this from that app just now. If you didn't, deny.</p>
<form method="post" action="/oauth/authorize">
<input type="hidden" name="request" value="{{.Request.ID}}">
{{if .Projects}}
<p>It will get <strong>{{.Scope}}</strong> access to {{range $i, $p := .Projects}}{{if $i}}, {{end}}{{$p}}{{end}}.</p>
{{else}}
{{if .Pairings}}
<label for="pairing">Sign-in window</label>
<select id="pairing" name="pairing">{{range .Pairings}}<option value="{{.ID}}">{{.Name}}: {{.Scope}} on {{range $i, $p := .Projects}}{{if $i}}, {{end}}{{$p}}{{end}} (closes in {{until .ExpiresAt}})</option>{{end}}</select>
{{else}}
<div class="msg">No sign-in window is open. Open one on the gateway page first.</div>
{{end}}
{{end}}
{{if not .LoggedIn}}
<label for="owner_token">Owner token</label>
<input type="password" id="owner_token" name="owner_token" autocomplete="current-password">
{{end}}
<div class="actions">
<button class="primary" name="decision" value="approve">Approve</button>
<button class="danger" name="decision" value="deny">Deny</button>
</div>
</form>
</div>
{{template "foot"}}{{end}}
`))
