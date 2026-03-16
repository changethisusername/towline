package proxy

type Backend string

const (
	BackendTraefik    Backend = "traefik"
	BackendCaddy      Backend = "caddy"
	BackendCloudflare Backend = "cloudflare"
	BackendNone       Backend = ""
)

type DomainMapping struct {
	Service string  `json:"service"`
	Domain  string  `json:"domain"`
	Port    int     `json:"port"`
	Method  Backend `json:"method"`
}

type Manager interface {
	List(composeContent string) ([]DomainMapping, error)
	Add(composeContent, service, domain string, port int) (string, error)
	Remove(composeContent, service, domain string) (string, error)
}
