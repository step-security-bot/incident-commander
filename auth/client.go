package auth

import (
	client "github.com/ory/client-go"
	"gorm.io/gorm"
)

type KratosHandler struct {
	client      *client.APIClient
	adminClient *client.APIClient
	jwtSecret   string
	db          *gorm.DB
}

func NewKratosHandler(kratosAPI, kratosAdminAPI, jwtSecret string, db *gorm.DB) *KratosHandler {
	return &KratosHandler{
		client:      newAPIClient(kratosAPI),
		adminClient: newAdminAPIClient(kratosAdminAPI),
		jwtSecret:   jwtSecret,
		db:          db,
	}
}

func newAPIClient(kratosAPI string) *client.APIClient {
	return newKratosClient(kratosAPI)
}

func newAdminAPIClient(kratosAdminAPI string) *client.APIClient {
	return newKratosClient(kratosAdminAPI)
}

func newKratosClient(apiURL string) *client.APIClient {
	configuration := client.NewConfiguration()
	configuration.Servers = []client.ServerConfiguration{{URL: apiURL}}
	return client.NewAPIClient(configuration)
}
