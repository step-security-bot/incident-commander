package teams

import (
	"encoding/json"
	"time"

	"github.com/flanksource/incident-commander/api"
	"github.com/flanksource/incident-commander/db"
	"github.com/flanksource/incident-commander/db/models"
	"github.com/flanksource/incident-commander/utils"
	"github.com/google/uuid"
	"github.com/patrickmn/go-cache"
)

var teamSpecCache = cache.New(time.Hour*1, time.Hour*1)

func GetTeamComponentsFromSelectors(teamID uuid.UUID, componentSelectors []api.ComponentSelector) []api.TeamComponent {
	var selectedComponents = make(map[string][]uuid.UUID)
	for _, compSelector := range componentSelectors {
		selectedComponents[utils.GetHash(compSelector)] = db.GetComponentsWithSelector(compSelector)
	}

	var teamComps []api.TeamComponent
	for hash, selectedComps := range selectedComponents {
		for _, compID := range selectedComps {
			teamComps = append(teamComps,
				api.TeamComponent{
					TeamID:      teamID,
					SelectorID:  hash,
					ComponentID: compID,
				},
			)
		}
	}
	return teamComps
}

func GetTeamSpec(ctx *api.Context, id string) (*api.TeamSpec, error) {
	if val, found := teamSpecCache.Get(id); found {
		return val.(*api.TeamSpec), nil
	}

	var team models.Team
	if err := ctx.DB().Where("id = ?", id).Find(&team).Error; err != nil {
		return nil, err
	}

	b, err := json.Marshal(team.Spec)
	if err != nil {
		return nil, err
	}

	var teamSpec api.TeamSpec
	if err := json.Unmarshal(b, &teamSpec); err != nil {
		return nil, err
	}

	teamSpecCache.Set(id, &teamSpec, cache.DefaultExpiration)

	return &teamSpec, nil
}

func PurgeCache(id string) {
	teamSpecCache.Delete(id)
}
