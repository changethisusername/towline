# AI Stack Operations

This stack runs Ollama for local LLM inference and Open WebUI as a chat interface.

## Common tasks

### Pull a model
Use towline_exec with service "ollama" and command "ollama pull llama3.2"

### List available models
Use towline_exec with service "ollama" and command "ollama list"

### Check GPU availability
Use towline_exec with service "ollama" and command "ollama ps"

### Restart Ollama after config changes
The Ollama service reads environment variables at startup. After changing
OLLAMA_* variables with towline_env_set, restart the service.

## Troubleshooting

### Ollama container exits immediately
Check for GPU driver issues. If the host doesn't have NVIDIA Container Toolkit
installed, remove the GPU reservation from the compose deploy section and use
CPU-only inference.

### Open WebUI can't connect to Ollama
Verify both services are running with towline_service_health. The connection
URL is http://ollama:11434 (Docker service name, not localhost).

### Models download slowly
Models are stored in the ollama_data volume. First pull is slow; subsequent
starts reuse the cached model. Consider pulling models before switching to
prod tier.
