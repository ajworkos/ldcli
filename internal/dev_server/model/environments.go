package model

import (
	"context"

	"github.com/launchdarkly/ldcli/internal/dev_server/adapters"
)

type Environment struct {
	Key  string
	Name string
}

func GetEnvironmentsForProject(ctx context.Context, projectKey string, query string, limit *int) ([]Environment, error) {
	store := StoreFromContext(ctx)
	project, err := store.GetDevProject(ctx, projectKey)
	if err != nil {
		return nil, err
	}
	
	apiAdapter := adapters.GetApi(ctx)
	environments, err := apiAdapter.GetProjectEnvironments(ctx, project.GetCloudProjectKey(), query, limit)
	if err != nil {
		return nil, err
	}

	var allEnvironments []Environment
	for _, environment := range environments {
		allEnvironments = append(allEnvironments, Environment{
			Key:  environment.Key,
			Name: environment.Name,
		})
	}

	return allEnvironments, nil
}
