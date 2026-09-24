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
	// Projects created before name validation may not match the init
	// rules, so only reject names that could point outside the projects dir.
	if projectName == "" || projectName == "." || projectName == ".." || strings.ContainsAny(projectName, `/\`) {
		return fmt.Errorf("invalid project name %q", projectName)
	}

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

	// towline.json lives in the project directory, which the project's agent
	// can write. Its IDs are only used after checking, against Portainer,
	// that each resource carries the name this project would have given it,
	// so the admin key can never be pointed at another project's resources.
	if projectCfg.StackName != projectName+"-dev" && projectCfg.StackName != projectName+"-prod" {
		return fmt.Errorf("towline.json stack_name %q does not belong to project %q; refusing to destroy", projectCfg.StackName, projectName)
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
	api := newAdminAPI(globalCfg)

	// Delete stack
	if projectCfg.StackID > 0 {
		fmt.Printf("Deleting stack '%s' (ID: %d)... ", projectCfg.StackName, projectCfg.StackID)
		stack, err := api.GetStack(projectCfg.StackID)
		switch {
		case err != nil:
			fmt.Printf("warning: %v\n", err)
		case stack.Name != projectCfg.StackName:
			fmt.Printf("skipped: stack %d is named %q, not %q\n", projectCfg.StackID, stack.Name, projectCfg.StackName)
		default:
			if err := api.DeleteStack(stack.ID, stack.EndpointID); err != nil {
				fmt.Printf("warning: %v\n", err)
			} else {
				fmt.Println("OK")
			}
		}
	}

	// Delete service user
	if projectCfg.UserID > 0 {
		fmt.Printf("Deleting service user (ID: %d)... ", projectCfg.UserID)
		want := "towline-" + projectCfg.StackName
		name, err := api.GetUsername(projectCfg.UserID)
		switch {
		case err != nil:
			fmt.Printf("warning: %v\n", err)
		case name != want:
			fmt.Printf("skipped: user %d is %q, not %q\n", projectCfg.UserID, name, want)
		default:
			if err := api.DeleteUser(projectCfg.UserID); err != nil {
				fmt.Printf("warning: %v\n", err)
			} else {
				fmt.Println("OK")
			}
		}
	}

	// Delete team
	if projectCfg.TeamID > 0 {
		fmt.Printf("Deleting team (ID: %d)... ", projectCfg.TeamID)
		want := "team-" + projectCfg.StackName
		name, err := api.GetTeamName(projectCfg.TeamID)
		switch {
		case err != nil:
			fmt.Printf("warning: %v\n", err)
		case name != want:
			fmt.Printf("skipped: team %d is %q, not %q\n", projectCfg.TeamID, name, want)
		default:
			if err := api.DeleteTeam(projectCfg.TeamID); err != nil {
				fmt.Printf("warning: %v\n", err)
			} else {
				fmt.Println("OK")
			}
		}
	}

	fmt.Println()
	fmt.Printf("Portainer resources for '%s' have been destroyed.\n", projectName)
	fmt.Printf("Local files remain at: %s\n", projectDir)
	fmt.Println("Remove them manually if no longer needed: rm -rf " + projectDir)

	return nil
}
