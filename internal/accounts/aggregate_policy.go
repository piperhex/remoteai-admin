package accounts

import "math"

func validateAggregatePolicy(policy object) error {
	if err := validateAggregatePolicyNumbers(policy); err != nil {
		return err
	}
	for field, value := range policy {
		switch field {
		case "maxAttempts", "failureThreshold", "initialCooldownSeconds", "maxCooldownSeconds",
			"recoverySuccessThreshold", "errorRateThresholdPercent", "errorRateMinRequests":
		case "enabled", "retryOnNetworkErrors", "respectRetryAfter":
			if _, valid := value.(bool); !valid {
				return invalidAggregate()
			}
		case "routingStrategy":
			if value != "balanced" && value != "priority" {
				return invalidAggregate()
			}
		case "retryStatusCodes":
			if err := validateAggregateStatusCodes(value); err != nil {
				return err
			}
		default:
			return invalidAggregate()
		}
	}
	return nil
}

func validateAggregatePolicyNumbers(policy object) error {
	limits := map[string][2]float64{
		"maxAttempts": {1, 20}, "failureThreshold": {1, 10},
		"initialCooldownSeconds": {1, 300}, "maxCooldownSeconds": {1, 3600},
		"recoverySuccessThreshold": {1, 10}, "errorRateThresholdPercent": {1, 100},
		"errorRateMinRequests": {1, 100},
	}
	for field, bounds := range limits {
		value, present := policy[field]
		if !present || (field == "errorRateThresholdPercent" && value == nil) {
			continue
		}
		number, valid := value.(float64)
		if !valid || math.Trunc(number) != number || number < bounds[0] || number > bounds[1] {
			return invalidAggregate()
		}
	}
	initial := 5.0
	maximum := 60.0
	if value, present := policy["initialCooldownSeconds"]; present {
		initial = number(value)
	}
	if value, present := policy["maxCooldownSeconds"]; present {
		maximum = number(value)
	}
	if maximum < initial {
		return invalidAggregate()
	}
	return nil
}

func validateAggregateStatusCodes(value interface{}) error {
	codes, valid := value.([]interface{})
	if !valid || len(codes) > 32 {
		return invalidAggregate()
	}
	for _, value := range codes {
		code, valid := value.(float64)
		if !valid || math.Trunc(code) != code || code < 400 || code > 599 {
			return invalidAggregate()
		}
	}
	return nil
}
