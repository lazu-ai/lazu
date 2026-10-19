package publicsite

// Unknown catalog prices are not free prices.
func publicPrice(value *float64) string {
	if value == nil {
		return "—"
	}
	return MoneyShort(*value)
}
