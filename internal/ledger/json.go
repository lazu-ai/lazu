package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
)

// Redis's bundled Lua cjson writes an empty array as {}. Accept only that
// empty object for array fields; malformed nonempty objects remain errors.
func (input *Input) UnmarshalJSON(raw []byte) error {
	type plain Input
	value := struct {
		*plain
		Budgets        json.RawMessage `json:"budgets"`
		Generations    json.RawMessage `json:"generations"`
		Rates          json.RawMessage `json:"rates"`
		Amount         json.RawMessage `json:"amount"`
		PricingVersion json.RawMessage `json:"pricing_version"`
	}{plain: (*plain)(input)}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var err error
	if input.Amount, err = decodeInteger(value.Amount); err != nil {
		return fmt.Errorf("amount: %w", err)
	}
	if input.PricingVersion, err = decodeInteger(value.PricingVersion); err != nil {
		return fmt.Errorf("pricing_version: %w", err)
	}
	input.Budgets = []Budget{}
	input.Generations = []Generation{}
	input.Rates = []RateLimit{}
	if len(value.Budgets) > 0 && !bytes.Equal(bytes.TrimSpace(value.Budgets), []byte("{}")) {
		if err := json.Unmarshal(value.Budgets, &input.Budgets); err != nil {
			return err
		}
	}
	if len(value.Generations) > 0 && !bytes.Equal(bytes.TrimSpace(value.Generations), []byte("{}")) {
		if err := json.Unmarshal(value.Generations, &input.Generations); err != nil {
			return err
		}
	}
	if len(value.Rates) > 0 && !bytes.Equal(bytes.TrimSpace(value.Rates), []byte("{}")) {
		if err := json.Unmarshal(value.Rates, &input.Rates); err != nil {
			return err
		}
	}
	return nil
}
func (event *Event) UnmarshalJSON(raw []byte) error {
	if err := json.Unmarshal(raw, &event.Input); err != nil {
		return err
	}
	var fields struct {
		OperationID       string `json:"operation_id"`
		Kind              string `json:"kind"`
		TelemetryEnvelope string `json:"telemetry_envelope"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	event.OperationID = fields.OperationID
	event.Kind = fields.Kind
	event.TelemetryEnvelope = fields.TelemetryEnvelope
	return nil
}

// Legacy records used JSON numbers (including cjson exponent notation). Parse
// their supplied decimal value exactly; never decode through float64.
func decodeInteger(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	text := string(bytes.TrimSpace(raw))
	if len(text) > 0 && text[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
		return strconv.ParseInt(value, 10, 64)
	}
	value, ok := new(big.Rat).SetString(text)
	if !ok || !value.IsInt() || !value.Num().IsInt64() {
		return 0, fmt.Errorf("invalid integral decimal %q", text)
	}
	return value.Num().Int64(), nil
}
func (budget *Budget) UnmarshalJSON(raw []byte) error {
	type plain Budget
	value := struct {
		*plain
		Limit json.RawMessage `json:"limit"`
		Spent json.RawMessage `json:"spent"`
	}{plain: (*plain)(budget)}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var err error
	if budget.Limit, err = decodeInteger(value.Limit); err != nil {
		return fmt.Errorf("budget limit: %w", err)
	}
	if budget.Spent, err = decodeInteger(value.Spent); err != nil {
		return fmt.Errorf("budget spent: %w", err)
	}
	return nil
}
func (rate *RateLimit) UnmarshalJSON(raw []byte) error {
	type plain RateLimit
	value := struct {
		*plain
		Limit json.RawMessage `json:"limit"`
		Cost  json.RawMessage `json:"cost"`
	}{plain: (*plain)(rate)}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var err error
	if rate.Limit, err = decodeInteger(value.Limit); err != nil {
		return fmt.Errorf("rate limit: %w", err)
	}
	if rate.Cost, err = decodeInteger(value.Cost); err != nil {
		return fmt.Errorf("rate cost: %w", err)
	}
	return nil
}
func (snapshot Snapshot) MarshalJSON() ([]byte, error) {
	token := make(map[int]string, len(snapshot.TokenRemaining))
	for id, value := range snapshot.TokenRemaining {
		token[id] = strconv.FormatInt(value, 10)
	}
	convert := func(values map[string]int64) map[string]string {
		result := make(map[string]string, len(values))
		for key, value := range values {
			result[key] = strconv.FormatInt(value, 10)
		}
		return result
	}
	return json.Marshal(struct {
		Balance            string
		CreditSeq          string
		TokenRemaining     map[int]string
		Budgets            map[string]string
		BudgetReserved     map[string]string
		ReservationBudgets map[string][]string
	}{strconv.FormatInt(snapshot.Balance, 10), strconv.FormatInt(snapshot.CreditSeq, 10), token, convert(snapshot.Budgets), convert(snapshot.BudgetReserved), snapshot.ReservationBudgets})
}
