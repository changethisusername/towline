package towline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseContainersForStack(t *testing.T) {
	data := []byte(`[
		{
			"Id": "abc123",
			"Names": ["/mystack-web-1"],
			"State": "running",
			"Status": "Up 2 hours",
			"Labels": {
				"com.docker.compose.project": "mystack",
				"com.docker.compose.service": "web"
			}
		},
		{
			"Id": "def456",
			"Names": ["/mystack-db-1"],
			"State": "running",
			"Status": "Up 2 hours",
			"Labels": {
				"com.docker.compose.project": "mystack",
				"com.docker.compose.service": "db"
			}
		},
		{
			"Id": "ghi789",
			"Names": ["/otherstack-web-1"],
			"State": "running",
			"Status": "Up 1 hour",
			"Labels": {
				"com.docker.compose.project": "otherstack",
				"com.docker.compose.service": "web"
			}
		}
	]`)

	containers, err := parseContainersForStack(data, "mystack")
	require.NoError(t, err)
	assert.Len(t, containers, 2)

	assert.Equal(t, "abc123", containers[0].ID)
	assert.Equal(t, "web", containers[0].Service)
	assert.Equal(t, "running", containers[0].State)

	assert.Equal(t, "def456", containers[1].ID)
	assert.Equal(t, "db", containers[1].Service)
}

func TestParseContainersForStack_InvalidJSON(t *testing.T) {
	_, err := parseContainersForStack([]byte(`not json`), "mystack")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse containers response")
}

func TestParseContainersForStack_NoMatch(t *testing.T) {
	data := []byte(`[
		{
			"Id": "abc123",
			"Names": ["/other-web-1"],
			"State": "running",
			"Status": "Up 2 hours",
			"Labels": {
				"com.docker.compose.project": "other",
				"com.docker.compose.service": "web"
			}
		}
	]`)

	containers, err := parseContainersForStack(data, "mystack")
	require.NoError(t, err)
	assert.Empty(t, containers)
}

func TestFilterByService(t *testing.T) {
	containers := []ContainerInfo{
		{ID: "abc123", Service: "web", State: "running"},
		{ID: "def456", Service: "db", State: "running"},
		{ID: "ghi789", Service: "web", State: "exited"},
	}

	webContainers := filterByService(containers, "web")
	assert.Len(t, webContainers, 2)
	assert.Equal(t, "abc123", webContainers[0].ID)
	assert.Equal(t, "ghi789", webContainers[1].ID)

	dbContainers := filterByService(containers, "db")
	assert.Len(t, dbContainers, 1)
	assert.Equal(t, "def456", dbContainers[0].ID)

	noContainers := filterByService(containers, "redis")
	assert.Empty(t, noContainers)
}
