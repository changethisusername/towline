package cli

import (
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
