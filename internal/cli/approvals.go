package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/changethisusername/towline/internal/approval"
	"github.com/changethisusername/towline/pkg/config"
)

const defaultApprovalListen = "127.0.0.1:8787"

func runApprovals(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: towline approvals <setup|serve|list|approve|reject>")
	}
	switch args[0] {
	case "setup":
		return runApprovalsSetup(args[1:])
	case "serve":
		return runApprovalsServe(args[1:])
	case "list":
		return runApprovalsList(args[1:])
	case "approve":
		return runApprovalsDecide(args[1:], "approve")
	case "reject":
		return runApprovalsDecide(args[1:], "reject")
	default:
		return fmt.Errorf("unknown approvals command: %s", args[0])
	}
}

// approvalOptions are the settings configureApprovals applies.
type approvalOptions struct {
	Mode         string // human or agent
	Listen       string // built-in server listen address
	ExternalURL  string // use an external approval server instead
	WebhookToken string // token for an external server (generated if empty)
	NtfyURL      string
	PublicURL    string
}

// configureApprovals updates cfg.Approval. For human mode with the built-in
// server it returns a newly generated approver token, which must be shown to
// the human once: only its hash is stored.
func configureApprovals(cfg *config.GlobalConfig, opts approvalOptions) (approverToken string, err error) {
	switch opts.Mode {
	case config.ApprovalModeAgent:
		cfg.Approval.Mode = config.ApprovalModeAgent
		return "", nil
	case config.ApprovalModeHuman:
	default:
		return "", fmt.Errorf("approval mode must be %q or %q", config.ApprovalModeHuman, config.ApprovalModeAgent)
	}

	a := &cfg.Approval
	a.Mode = config.ApprovalModeHuman
	a.NtfyURL = opts.NtfyURL
	a.PublicURL = opts.PublicURL

	if opts.ExternalURL != "" {
		if _, err := approval.NewWebhook(opts.ExternalURL, ""); err != nil {
			return "", err
		}
		a.URL = opts.ExternalURL
		a.Listen = ""
		a.ApproverTokenHash = ""
		a.WebhookToken = opts.WebhookToken
		return "", nil
	}

	listen := opts.Listen
	if listen == "" {
		listen = defaultApprovalListen
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: %w", listen, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	_, port, _ := net.SplitHostPort(listen)
	a.Listen = listen
	a.URL = "http://" + net.JoinHostPort(host, port)
	if a.WebhookToken == "" || opts.WebhookToken != "" {
		a.WebhookToken = opts.WebhookToken
		if a.WebhookToken == "" {
			a.WebhookToken = approval.NewToken()
		}
	}
	approverToken = approval.NewToken()
	a.ApproverTokenHash = approval.HashToken(approverToken)
	return approverToken, nil
}

func runApprovalsSetup(args []string) error {
	fs := flag.NewFlagSet("approvals setup", flag.ContinueOnError)
	mode := fs.String("mode", "", "human (approval server) or agent (the agent confirms its own prod operations)")
	listen := fs.String("listen", defaultApprovalListen, "Listen address for the built-in approval server")
	external := fs.String("url", "", "Use an external approval server at this URL instead of the built-in one")
	webhookToken := fs.String("webhook-token", "", "Bearer token towline-mcp sends to the approval server (generated if empty)")
	ntfy := fs.String("ntfy", "", "ntfy topic URL for push notifications, e.g. https://ntfy.sh/<private-topic>")
	publicURL := fs.String("public-url", "", "URL where you open the approval UI (used in notification links)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	m := *mode
	if m == "" {
		m, err = promptApprovalMode(bufio.NewReader(os.Stdin))
		if err != nil {
			return err
		}
	}

	token, err := configureApprovals(cfg, approvalOptions{
		Mode: m, Listen: *listen, ExternalURL: *external, WebhookToken: *webhookToken,
		NtfyURL: *ntfy, PublicURL: *publicURL,
	})
	if err != nil {
		return err
	}
	if err := config.SaveGlobalConfig(cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	printApprovalSummary(cfg, token)
	return nil
}

// promptApprovalMode asks how prod operations should be approved.
func promptApprovalMode(reader *bufio.Reader) (string, error) {
	fmt.Println("How should prod-tier operations (deploys, config changes, exec) be approved?")
	fmt.Println("  [1] By a human, in Towline's approval server (recommended)")
	fmt.Println("  [2] By the agent itself: it confirms each operation (for autonomous/agentic setups)")
	fmt.Print("Choice [1]: ")
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	switch strings.TrimSpace(answer) {
	case "", "1":
		return config.ApprovalModeHuman, nil
	case "2":
		return config.ApprovalModeAgent, nil
	}
	return "", fmt.Errorf("invalid choice %q", strings.TrimSpace(answer))
}

func printApprovalSummary(cfg *config.GlobalConfig, approverToken string) {
	fmt.Println()
	if cfg.Approval.Mode == config.ApprovalModeAgent {
		fmt.Println("Prod approvals: agent mode. The agent confirms its own prod operations.")
	} else if approverToken == "" {
		fmt.Printf("Prod approvals: human, via the external approval server at %s.\n", cfg.Approval.URL)
	} else {
		fmt.Println("Prod approvals: human, via Towline's approval server.")
		fmt.Println()
		fmt.Println("Your approver token (shown only once; store it in your password manager):")
		fmt.Println()
		fmt.Println("  " + approverToken)
		fmt.Println()
		fmt.Println("Keep it away from your agents: anyone with it can approve prod changes.")
		fmt.Println()
		fmt.Println("Start the server and keep it running:  towline approvals serve")
		fmt.Printf("Then approve requests at %s/ui or with 'towline approvals approve <id>'.\n", cfg.Approval.URL)
	}
	fmt.Println()
	fmt.Println("Apply this to existing projects with:  towline refresh --all")
}

func runApprovalsServe(args []string) error {
	fs := flag.NewFlagSet("approvals serve", flag.ContinueOnError)
	listen := fs.String("listen", "", "Override the configured listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}
	a := cfg.Approval
	if a.Mode != config.ApprovalModeHuman || a.ApproverTokenHash == "" {
		return fmt.Errorf("the built-in approval server is not configured; run 'towline approvals setup' and choose human approval")
	}
	addr := a.Listen
	if *listen != "" {
		addr = *listen
	}
	if addr == "" {
		addr = defaultApprovalListen
	}

	srv, err := approval.NewServer(approval.ServerConfig{
		WebhookToken:      a.WebhookToken,
		ApproverTokenHash: a.ApproverTokenHash,
		NtfyURL:           a.NtfyURL,
		PublicURL:         a.PublicURL,
	})
	if err != nil {
		return err
	}

	if host, _, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			fmt.Fprintln(os.Stderr, "Warning: the approval server is listening beyond localhost without TLS; put it behind an HTTPS reverse proxy.")
		}
	}

	fmt.Printf("Towline approval server listening on http://%s (UI at /ui)\n", addr)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	return httpSrv.ListenAndServe()
}

// approverClient calls the approval server's approver API.
type approverClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func newApproverClient() (*approverClient, error) {
	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Approval.Mode != config.ApprovalModeHuman || cfg.Approval.URL == "" {
		return nil, fmt.Errorf("human approvals are not configured; run 'towline approvals setup'")
	}
	// Read the token from the terminal rather than an env var or flag, which
	// agents running in the same shell could see.
	fmt.Fprint(os.Stderr, "Approver token: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to read token: %w", err)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return nil, fmt.Errorf("approver token is required")
	}
	return &approverClient{
		baseURL: strings.TrimRight(cfg.Approval.URL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *approverClient) do(method, path string, out any) error {
	req, err := http.NewRequest(method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach approval server (is 'towline approvals serve' running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		return fmt.Errorf("approval server: %s (status %d)", e.Error, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func runApprovalsList(args []string) error {
	c, err := newApproverClient()
	if err != nil {
		return err
	}
	var records []approval.Record
	if err := c.do("GET", "/api/requests", &records); err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("No approval requests.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tPROJECT\tACTION\tREQUESTED")
	for _, r := range records {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.Status, r.Project, r.Action, r.CreatedAt.Local().Format(time.DateTime))
	}
	return w.Flush()
}

func runApprovalsDecide(args []string, decision string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: towline approvals %s <id>", decision)
	}
	c, err := newApproverClient()
	if err != nil {
		return err
	}
	if err := c.do("POST", "/"+url.PathEscape(args[0])+"/"+decision, nil); err != nil {
		return err
	}
	past := map[string]string{"approve": "approved", "reject": "rejected"}[decision]
	fmt.Printf("Request %s %s.\n", args[0], past)
	return nil
}
