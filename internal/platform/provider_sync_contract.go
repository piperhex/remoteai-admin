package platform

const providerFieldTimestampMaxLength = 40

// Extend the Go API without changing the frozen NestJS contract or its generated snapshot.
func (contract *Contract) extendProviderSyncContract() {
	provider := contract.Schemas["SyncProviderDto"]
	provider.Fields = append(provider.Fields, Field{
		Name:  "requestOptions",
		Rules: []Rule{{Name: "IsObject"}, {Name: "IsOptional"}},
	})
	contract.Schemas["SyncProviderDto"] = provider
	for _, name := range []string{"balanceScript", "marketSource"} {
		provider.Fields = append(provider.Fields, Field{
			Name: name, Rules: []Rule{{Name: "IsObject"}, {Name: "IsOptional"}},
		})
	}
	contract.Schemas["SyncProviderDto"] = provider

	versions := contract.Schemas["ProviderFieldModifiedAtDto"]
	versions.Fields = append(versions.Fields, Field{
		Name: "requestOptions",
		Rules: []Rule{
			{Name: "MaxLength", Args: []interface{}{float64(providerFieldTimestampMaxLength)}},
			{Name: "IsString"},
			{Name: "IsOptional"},
		},
	})
	contract.Schemas["ProviderFieldModifiedAtDto"] = versions
	for _, name := range []string{"balanceScript", "marketSource"} {
		versions.Fields = append(versions.Fields, Field{
			Name: name, Rules: []Rule{
				{Name: "MaxLength", Args: []interface{}{float64(providerFieldTimestampMaxLength)}},
				{Name: "IsString"}, {Name: "IsOptional"},
			},
		})
	}
	contract.Schemas["ProviderFieldModifiedAtDto"] = versions
}
