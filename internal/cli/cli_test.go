package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_NoArgs(t *testing.T) {
	err := Run(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage:")
}

func TestRun_EmptyArgs(t *testing.T) {
	err := Run([]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "usage:")
}

func TestRun_UnknownCommand(t *testing.T) {
	err := Run([]string{"foobar"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
	assert.Contains(t, err.Error(), "foobar")
}

func TestRun_Setup(t *testing.T) {
	// setup reads from stdin, so it will fail, but it should not panic.
	// It will attempt to read from stdin and fail or prompt for input.
	// We set HOME to a temp dir to avoid touching real config.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	err := Run([]string{"setup"})
	// setup reads from stdin and will get an error, but should not panic
	require.Error(t, err)
}

func TestRun_List(t *testing.T) {
	// list calls LoadGlobalConfig which needs a config file.
	// With a temp HOME, it won't find one.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	err := Run([]string{"list"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "towline not configured")
}

func TestRun_HelpFlagIsNotAnError(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"setup --help", []string{"setup", "--help"}},
		{"setup -h", []string{"setup", "-h"}},
		{"init --help", []string{"init", "--help"}},
		{"destroy -h", []string{"destroy", "-h"}},
		{"refresh --help", []string{"refresh", "--help"}},
		{"update --help", []string{"update", "--help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			assert.NoError(t, Run(tt.args))
		})
	}
}

func TestRun_SetupRejectsArguments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	assert.ErrorContains(t, Run([]string{"setup", "extra"}), "no arguments")
	assert.Error(t, Run([]string{"setup", "--bogus"}))
}

func TestAbsProjectsDir(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"tilde alone", "~", "/home/u"},
		{"tilde path", "~/projects", "/home/u/projects"},
		{"absolute", "/srv/projects/", "/srv/projects"},
		{"relative", "projects", filepath.Join(cwd, "projects")},
		{"tilde in the middle is literal", "a/~/b", filepath.Join(cwd, "a/~/b")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := absProjectsDir(tt.in, "/home/u")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRun_AllCommandsDispatch(t *testing.T) {
	// Verify each known command dispatches without panicking.
	// They will all return errors (not configured / not implemented / stdin EOF),
	// but the important thing is they dispatch correctly.
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	commands := []string{
		"setup",
		"init",
		"list",
		"destroy",
		"promote",
		"rotate-keys",
		"status",
	}

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			err := Run([]string{cmd})
			// All commands should return an error (not configured, not implemented, or stdin EOF)
			// but none should panic
			require.Error(t, err)
		})
	}
}
