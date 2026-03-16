package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/changethisusername/towline/pkg/config"
)

func runList(args []string) error {
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	projectsDir := globalCfg.ProjectsDir

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No projects directory found. Run 'towline init <project>' to create a project.")
			return nil
		}
		return fmt.Errorf("failed to read projects directory: %w", err)
	}

	type projectInfo struct {
		name      string
		stackName string
		tier      string
		status    string
	}

	var projects []projectInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		projDir := filepath.Join(projectsDir, entry.Name())
		cfg, err := config.LoadProjectConfig(projDir)
		if err != nil {
			continue // Not a towline project
		}

		projects = append(projects, projectInfo{
			name:      entry.Name(),
			stackName: cfg.StackName,
			tier:      cfg.Tier,
			status:    "configured",
		})
	}

	if len(projects) == 0 {
		fmt.Println("No towline projects found. Run 'towline init <project>' to create one.")
		return nil
	}

	// Print table header
	fmt.Printf("%-20s %-25s %-6s %-12s\n", "PROJECT", "STACK", "TIER", "STATUS")
	fmt.Println(strings.Repeat("-", 65))

	for _, p := range projects {
		fmt.Printf("%-20s %-25s %-6s %-12s\n", p.name, p.stackName, p.tier, p.status)
	}

	return nil
}
