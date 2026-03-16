package config

type GlobalConfig struct {
	PortainerURL    string `yaml:"portainer_url"`
	PortainerAPIKey string `yaml:"portainer_admin_key"`
	PortainerEnvID  int    `yaml:"portainer_env_id"`
	ProjectsDir     string `yaml:"projects_dir"`
}

type ProjectConfig struct {
	StackName string  `json:"stack_name"`
	StackID   int     `json:"stack_id"`
	Tier      string  `json:"tier"`
	TeamID    int     `json:"team_id"`
	UserID    int     `json:"user_id"`
	EnvID     int     `json:"environment_id"`
	MCPBinary string  `json:"mcp_binary"`
	MCPArgs   MCPArgs `json:"mcp_args"`
}

type MCPArgs struct {
	Server string `json:"server"`
	Token  string `json:"token"`
	Stack  string `json:"stack"`
	Tier   string `json:"tier"`
}
