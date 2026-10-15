package accounts

import "github.com/codex-switch/admin-go/internal/platform"

// AggregateSummaries exposes only enabled choices, without member credentials or routing policies.
func AggregateSummaries(deps *platform.Dependencies, owner string) ([]platform.JSON, error) {
	payload, err := listAggregates(deps.DB, owner)
	if err != nil {
		return nil, err
	}
	return aggregateSummaries(payload), nil
}

func aggregateSummaries(payload aggregatePayload) []platform.JSON {
	result := []platform.JSON{}
	for _, entry := range payload.Aggregates {
		if entry.Config == nil || entry.Config["enabled"] != true {
			continue
		}
		result = append(result, platform.JSON{
			"id": "aggregate:" + entry.ID, "name": entry.Config["name"],
			"model": entry.Config["model"], "group": "", "kind": "aggregate",
		})
	}
	return result
}
