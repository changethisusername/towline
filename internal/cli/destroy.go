package cli

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/changethisusername/towline/pkg/config"
)

func runDestroy(args []string) error {
	fs := flag.NewFlagSet("destroy", flag.ContinueOnError)
	confirm := fs.Bool("confirm", false, "Skip interactive confirmation")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("usage: towline destroy [--confirm] <project-name>")
	}

	projectName := fs.Arg(0)

	// Load global config
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		return err
	}

	projectDir := filepath.Join(globalCfg.ProjectsDir, projectName)

	// Load project config
	projectCfg, err := config.LoadProjectConfig(projectDir)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	// Confirm destruction
	if !*confirm {
		fmt.Printf("This will destroy the Portainer stack '%s' and team for project '%s'.\n", projectCfg.StackName, projectName)
		fmt.Printf("Local files in %s will NOT be deleted.\n", projectDir)
		fmt.Print("\nType the project name to confirm: ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}
		input = strings.TrimSpace(input)

		if input != projectName {
			return fmt.Errorf("confirmation failed — expected '%s', got '%s'", projectName, input)
		}
	}

	// Initialize Portainer API
	api := config.NewPortainerAPI(globalCfg.PortainerURL)
	api.Token = globalCfg.PortainerAPIKey

	// Delete stack
	if projectCfg.StackID > 0 {
		fmt.Printf("Deleting stack '%s' (ID: %d)... ", projectCfg.StackName, projectCfg.StackID)
		if err := api.DeleteStack(projectCfg.StackID, projectCfg.EnvID); err != nil {
			fmt.Printf("warning: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	// Delete service user
	if projectCfg.UserID > 0 {
		fmt.Printf("Deleting service user (ID: %d)... ", projectCfg.UserID)
		if err := api.DeleteUser(projectCfg.UserID); err != nil {
			fmt.Printf("warning: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	// Delete team
	if projectCfg.TeamID > 0 {
		fmt.Printf("Deleting team (ID: %d)... ", projectCfg.TeamID)
		if err := api.DeleteTeam(projectCfg.TeamID); err != nil {
			fmt.Printf("warning: %v\n", err)
		} else {
			fmt.Println("OK")
		}
	}

	fmt.Println()
	fmt.Printf("Portainer resources for '%s' have been destroyed.\n", projectName)
	fmt.Printf("Local files remain at: %s\n", projectDir)
	fmt.Println("Remove them manually if no longer needed: rm -rf " + projectDir)

	return nil
}
