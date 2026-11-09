package platform

// Extend the Go API without changing the frozen legacy contract fixtures.
func (contract *Contract) extendAccountAliasContract() {
	details := contract.Schemas["AccountPrivateDetailsDto"]
	details.Fields = append(details.Fields, Field{
		Name: "alias",
		Rules: []Rule{
			{Name: "IsString"},
			{Name: "ValidateIf", Args: []interface{}{"(_object, value) => value !== undefined"}},
		},
	})
	// Keep omitted aliases absent for older clients. The accounts service trims and
	// validates length and control characters before storing an explicit value.
	contract.Schemas["AccountPrivateDetailsDto"] = details
}
