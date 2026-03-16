package towline

import (
	"net/http"

	"github.com/changethisusername/towline/pkg/portainer/models"
	"github.com/stretchr/testify/mock"
)

// mockPortainerClient is a mock implementation of the mcp.PortainerClient interface
// used for handler integration tests.
type mockPortainerClient struct {
	mock.Mock
}

// Tag methods

func (m *mockPortainerClient) GetEnvironmentTags() ([]models.EnvironmentTag, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.EnvironmentTag), args.Error(1)
}

func (m *mockPortainerClient) CreateEnvironmentTag(name string) (int, error) {
	args := m.Called(name)
	return args.Int(0), args.Error(1)
}

// Environment methods

func (m *mockPortainerClient) GetEnvironments() ([]models.Environment, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Environment), args.Error(1)
}

func (m *mockPortainerClient) UpdateEnvironmentTags(id int, tagIds []int) error {
	args := m.Called(id, tagIds)
	return args.Error(0)
}

func (m *mockPortainerClient) UpdateEnvironmentUserAccesses(id int, userAccesses map[int]string) error {
	args := m.Called(id, userAccesses)
	return args.Error(0)
}

func (m *mockPortainerClient) UpdateEnvironmentTeamAccesses(id int, teamAccesses map[int]string) error {
	args := m.Called(id, teamAccesses)
	return args.Error(0)
}

// Environment Group methods

func (m *mockPortainerClient) GetEnvironmentGroups() ([]models.Group, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Group), args.Error(1)
}

func (m *mockPortainerClient) CreateEnvironmentGroup(name string, environmentIds []int) (int, error) {
	args := m.Called(name, environmentIds)
	return args.Int(0), args.Error(1)
}

func (m *mockPortainerClient) UpdateEnvironmentGroupName(id int, name string) error {
	args := m.Called(id, name)
	return args.Error(0)
}

func (m *mockPortainerClient) UpdateEnvironmentGroupEnvironments(id int, environmentIds []int) error {
	args := m.Called(id, environmentIds)
	return args.Error(0)
}

func (m *mockPortainerClient) UpdateEnvironmentGroupTags(id int, tagIds []int) error {
	args := m.Called(id, tagIds)
	return args.Error(0)
}

// Access Group methods

func (m *mockPortainerClient) GetAccessGroups() ([]models.AccessGroup, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.AccessGroup), args.Error(1)
}

func (m *mockPortainerClient) CreateAccessGroup(name string, environmentIds []int) (int, error) {
	args := m.Called(name, environmentIds)
	return args.Int(0), args.Error(1)
}

func (m *mockPortainerClient) UpdateAccessGroupName(id int, name string) error {
	args := m.Called(id, name)
	return args.Error(0)
}

func (m *mockPortainerClient) UpdateAccessGroupUserAccesses(id int, userAccesses map[int]string) error {
	args := m.Called(id, userAccesses)
	return args.Error(0)
}

func (m *mockPortainerClient) UpdateAccessGroupTeamAccesses(id int, teamAccesses map[int]string) error {
	args := m.Called(id, teamAccesses)
	return args.Error(0)
}

func (m *mockPortainerClient) AddEnvironmentToAccessGroup(id int, environmentId int) error {
	args := m.Called(id, environmentId)
	return args.Error(0)
}

func (m *mockPortainerClient) RemoveEnvironmentFromAccessGroup(id int, environmentId int) error {
	args := m.Called(id, environmentId)
	return args.Error(0)
}

// Stack methods (Edge Stacks)

func (m *mockPortainerClient) GetStacks() ([]models.Stack, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Stack), args.Error(1)
}

func (m *mockPortainerClient) GetStackFile(id int) (string, error) {
	args := m.Called(id)
	return args.String(0), args.Error(1)
}

func (m *mockPortainerClient) CreateStack(name string, file string, environmentGroupIds []int) (int, error) {
	args := m.Called(name, file, environmentGroupIds)
	return args.Int(0), args.Error(1)
}

func (m *mockPortainerClient) UpdateStack(id int, file string, environmentGroupIds []int) error {
	args := m.Called(id, file, environmentGroupIds)
	return args.Error(0)
}

// Local Stack methods

func (m *mockPortainerClient) GetLocalStacks() ([]models.LocalStack, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.LocalStack), args.Error(1)
}

func (m *mockPortainerClient) GetLocalStackFile(id int) (string, error) {
	args := m.Called(id)
	return args.String(0), args.Error(1)
}

func (m *mockPortainerClient) CreateLocalStack(endpointId int, name, file string, env []models.LocalStackEnvVar) (int, error) {
	args := m.Called(endpointId, name, file, env)
	return args.Int(0), args.Error(1)
}

func (m *mockPortainerClient) UpdateLocalStack(id, endpointId int, file string, env []models.LocalStackEnvVar, prune, pullImage bool) error {
	args := m.Called(id, endpointId, file, env, prune, pullImage)
	return args.Error(0)
}

func (m *mockPortainerClient) StartLocalStack(id, endpointId int) error {
	args := m.Called(id, endpointId)
	return args.Error(0)
}

func (m *mockPortainerClient) StopLocalStack(id, endpointId int) error {
	args := m.Called(id, endpointId)
	return args.Error(0)
}

func (m *mockPortainerClient) DeleteLocalStack(id, endpointId int) error {
	args := m.Called(id, endpointId)
	return args.Error(0)
}

// Team methods

func (m *mockPortainerClient) CreateTeam(name string) (int, error) {
	args := m.Called(name)
	return args.Int(0), args.Error(1)
}

func (m *mockPortainerClient) GetTeams() ([]models.Team, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Team), args.Error(1)
}

func (m *mockPortainerClient) UpdateTeamName(id int, name string) error {
	args := m.Called(id, name)
	return args.Error(0)
}

func (m *mockPortainerClient) UpdateTeamMembers(id int, userIds []int) error {
	args := m.Called(id, userIds)
	return args.Error(0)
}

// User methods

func (m *mockPortainerClient) GetUsers() ([]models.User, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.User), args.Error(1)
}

func (m *mockPortainerClient) UpdateUserRole(id int, role string) error {
	args := m.Called(id, role)
	return args.Error(0)
}

// Settings methods

func (m *mockPortainerClient) GetSettings() (models.PortainerSettings, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return models.PortainerSettings{}, args.Error(1)
	}
	return args.Get(0).(models.PortainerSettings), args.Error(1)
}

func (m *mockPortainerClient) GetVersion() (string, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return "", args.Error(1)
	}
	return args.Get(0).(string), args.Error(1)
}

// Docker Proxy methods

func (m *mockPortainerClient) ProxyDockerRequest(opts models.DockerProxyRequestOptions) (*http.Response, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

// Kubernetes Proxy methods

func (m *mockPortainerClient) ProxyKubernetesRequest(opts models.KubernetesProxyRequestOptions) (*http.Response, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}
