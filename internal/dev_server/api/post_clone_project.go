package api

import (
	"context"

	"github.com/pkg/errors"

	"github.com/launchdarkly/ldcli/internal/dev_server/model"
)

func (s server) PostCloneProject(ctx context.Context, request PostCloneProjectRequestObject) (PostCloneProjectResponseObject, error) {
	if request.Body.SourceProjectKey == "" {
		return PostCloneProject400JSONResponse{
			ErrorResponseJSONResponse{
				Code:    "invalid_request",
				Message: "sourceProjectKey is required",
			},
		}, nil
	}

	includeOverrides := false
	if request.Body.IncludeOverrides != nil {
		includeOverrides = *request.Body.IncludeOverrides
	}

	store := model.StoreFromContext(ctx)
	project, err := model.CloneProject(ctx, request.Body.SourceProjectKey, request.ProjectKey, includeOverrides)
	switch {
	case errors.As(err, &model.ErrNotFound{}):
		return PostCloneProject404JSONResponse{
			Code:    "not_found",
			Message: err.Error(),
		}, nil
	case errors.As(err, &model.ErrAlreadyExists{}):
		return PostCloneProject409JSONResponse{
			Code:    "conflict",
			Message: err.Error(),
		}, nil
	case err != nil:
		return nil, err
	}

	response := ProjectJSONResponse{
		LastSyncedFromSource: project.LastSyncTime.Unix(),
		Context:              project.Context,
		SourceEnvironmentKey: project.SourceEnvironmentKey,
		FlagsState:           &project.AllFlagsState,
	}

	if request.Params.Expand != nil {
		for _, item := range *request.Params.Expand {
			if item == "overrides" {
				overrides, err := store.GetOverridesForProject(ctx, request.ProjectKey)
				if err != nil {
					return nil, err
				}
				respOverrides := make(model.FlagsState)
				for _, override := range overrides {
					if !override.Active {
						continue
					}
					respOverrides[override.FlagKey] = model.FlagState{
						Value:   override.Value,
						Version: override.Version,
					}
				}
				response.Overrides = &respOverrides
			}
			if item == "availableVariations" {
				availableVariations, err := store.GetAvailableVariationsForProject(ctx, request.ProjectKey)
				if err != nil {
					return nil, err
				}
				respAvailableVariations := availableVariationsToResponseFormat(availableVariations)
				response.AvailableVariations = &respAvailableVariations
			}
		}

	}

	return PostCloneProject201JSONResponse{
		response,
	}, nil
}

