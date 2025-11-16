package model

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/ldcli/internal/dev_server/adapters"
)

type Project struct {
	Key                  string
	SourceEnvironmentKey string
	SourceProjectKey     string // The cloud project to sync from (empty if Key should be used)
	Context              ldcontext.Context
	LastSyncTime         time.Time
	AllFlagsState        FlagsState
	AvailableVariations  []FlagVariation
}

// GetCloudProjectKey returns the cloud project key to use for API calls.
// For cloned projects, this returns SourceProjectKey. For regular projects, returns Key.
func (p *Project) GetCloudProjectKey() string {
	if p.SourceProjectKey != "" {
		return p.SourceProjectKey
	}
	return p.Key
}

// CreateProject creates a project and adds it to the database.
func CreateProject(ctx context.Context, projectKey, sourceEnvironmentKey string, ldCtx *ldcontext.Context) (Project, error) {
	project := Project{
		Key:                  projectKey,
		SourceEnvironmentKey: sourceEnvironmentKey,
	}

	if ldCtx == nil {
		project.Context = ldcontext.NewBuilder("user").Key("dev-environment").Build()
	} else {
		project.Context = *ldCtx
	}
	err := project.refreshExternalState(ctx)
	if err != nil {
		return Project{}, err
	}
	store := StoreFromContext(ctx)
	err = store.InsertProject(ctx, project)
	if err != nil {
		return Project{}, err
	}
	return project, nil
}

// CloneProject creates a copy of an existing project with a new name.
// The cloned project references the same cloud project for syncing.
func CloneProject(ctx context.Context, sourceKey, targetKey string, includeOverrides bool) (Project, error) {
	store := StoreFromContext(ctx)
	
	// Fetch source project
	sourceProject, err := store.GetDevProject(ctx, sourceKey)
	if err != nil {
		return Project{}, errors.Wrapf(err, "unable to get source project %s", sourceKey)
	}

	// Create new project as a clone
	clonedProject := Project{
		Key:                  targetKey,
		SourceEnvironmentKey: sourceProject.SourceEnvironmentKey,
		SourceProjectKey:     sourceProject.GetCloudProjectKey(), // Point to the cloud project
		Context:              sourceProject.Context,
		LastSyncTime:         time.Now(),
		AllFlagsState:        sourceProject.AllFlagsState,
		AvailableVariations:  sourceProject.AvailableVariations,
	}

	// Insert cloned project
	err = store.InsertProject(ctx, clonedProject)
	if err != nil {
		return Project{}, errors.Wrapf(err, "unable to insert cloned project %s", targetKey)
	}

	// Optionally clone overrides
	if includeOverrides {
		sourceOverrides, err := store.GetOverridesForProject(ctx, sourceKey)
		if err != nil {
			return Project{}, errors.Wrapf(err, "unable to get overrides for source project %s", sourceKey)
		}

		for _, override := range sourceOverrides {
			clonedOverride := Override{
				ProjectKey: targetKey,
				FlagKey:    override.FlagKey,
				Value:      override.Value,
				Active:     override.Active,
			}
			_, err := store.UpsertOverride(ctx, clonedOverride)
			if err != nil {
				return Project{}, errors.Wrapf(err, "unable to clone override for flag %s", override.FlagKey)
			}
		}
	}

	return clonedProject, nil
}

func (project *Project) refreshExternalState(ctx context.Context) error {
	flagsState, err := project.fetchFlagState(ctx)
	if err != nil {
		return err
	}
	project.AllFlagsState = flagsState
	project.LastSyncTime = time.Now()

	availableVariations, err := project.fetchAvailableVariations(ctx)
	if err != nil {
		return err
	}
	project.AvailableVariations = availableVariations
	return nil
}

func UpdateProject(ctx context.Context, projectKey string, context *ldcontext.Context, sourceEnvironmentKey *string) (Project, error) {
	store := StoreFromContext(ctx)
	project, err := store.GetDevProject(ctx, projectKey)
	if err != nil {
		return Project{}, err
	}
	if context != nil {
		project.Context = *context
	}

	if sourceEnvironmentKey != nil {
		project.SourceEnvironmentKey = *sourceEnvironmentKey
	}

	err = project.refreshExternalState(ctx)
	if err != nil {
		return Project{}, err
	}

	updated, err := store.UpdateProject(ctx, *project)
	if err != nil {
		return Project{}, err
	}
	if !updated {
		return Project{}, errors.New("Project not updated")
	}

	allFlagsWithOverrides, err := project.GetFlagStateWithOverridesForProject(ctx)
	if err != nil {
		return Project{}, errors.Wrapf(err, "unable to get overrides for project, %s", projectKey)
	}

	GetObserversFromContext(ctx).Notify(SyncEvent{
		ProjectKey:    project.Key,
		AllFlagsState: allFlagsWithOverrides,
	})
	return *project, nil
}

func (project Project) GetFlagStateWithOverridesForProject(ctx context.Context) (FlagsState, error) {
	store := StoreFromContext(ctx)
	overrides, err := store.GetOverridesForProject(ctx, project.Key)
	if err != nil {
		return FlagsState{}, errors.Wrapf(err, "unable to fetch overrides for project %s", project.Key)
	}
	withOverrides := make(FlagsState, len(project.AllFlagsState))
	for flagKey, flagState := range project.AllFlagsState {
		if override, ok := overrides.GetFlag(flagKey); ok {
			flagState = override.Apply(flagState)
		}
		withOverrides[flagKey] = flagState
	}
	return withOverrides, nil
}

func (project Project) fetchAvailableVariations(ctx context.Context) ([]FlagVariation, error) {
	apiAdapter := adapters.GetApi(ctx)
	flags, err := apiAdapter.GetAllFlags(ctx, project.GetCloudProjectKey())
	if err != nil {
		return nil, err
	}
	var allVariations []FlagVariation
	for _, flag := range flags {
		flagKey := flag.Key
		for _, variation := range flag.Variations {
			allVariations = append(allVariations, FlagVariation{
				FlagKey: flagKey,
				Variation: Variation{
					Id:          *variation.Id,
					Description: variation.Description,
					Name:        variation.Name,
					Value:       ldvalue.CopyArbitraryValue(variation.Value),
				},
			})
		}
	}
	return allVariations, nil
}

func (project Project) fetchFlagState(ctx context.Context) (FlagsState, error) {
	apiAdapter := adapters.GetApi(ctx)
	sdkKey, err := apiAdapter.GetSdkKey(ctx, project.GetCloudProjectKey(), project.SourceEnvironmentKey)
	flagsState := make(FlagsState)
	if err != nil {
		return flagsState, err
	}

	sdkAdapter := adapters.GetSdk(ctx)
	sdkFlags, err := sdkAdapter.GetAllFlagsState(ctx, project.Context, sdkKey)
	if err != nil {
		return flagsState, err
	}

	flagsState = FromAllFlags(sdkFlags)
	return flagsState, nil
}
