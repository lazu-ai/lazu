package billing

import (
	"database/sql/driver"
	"fmt"
	"strconv"

	"github.com/shopspring/decimal"
)

// MicroUSD is the integer billing unit used for wallet balances and charges.
// One USD is 1,000,000 MicroUSD.
type MicroUSD int64

// USD converts a dollar amount to MicroUSD at backend-owned dollar boundaries.
func USD(dollars float64) MicroUSD {
	return MicroUSD(decimal.NewFromFloat(dollars).
		Mul(decimal.NewFromInt(MicroUSDPerUSD)).
		Round(0).
		IntPart())
}

func (m MicroUSD) Dollars() float64 {
	return float64(m) / float64(MicroUSDPerUSD)
}

func (m MicroUSD) Int64() int64 {
	return int64(m)
}

func (m MicroUSD) Int() int {
	return int(m)
}

func (m MicroUSD) Value() (driver.Value, error) {
	return int64(m), nil
}

func (m *MicroUSD) Scan(value any) error {
	if value == nil {
		*m = 0
		return nil
	}
	switch v := value.(type) {
	case int:
		*m = MicroUSD(v)
	case int8:
		*m = MicroUSD(v)
	case int16:
		*m = MicroUSD(v)
	case int32:
		*m = MicroUSD(v)
	case int64:
		*m = MicroUSD(v)
	case uint:
		*m = MicroUSD(v)
	case uint8:
		*m = MicroUSD(v)
	case uint16:
		*m = MicroUSD(v)
	case uint32:
		*m = MicroUSD(v)
	case uint64:
		*m = MicroUSD(v)
	case float32:
		*m = MicroUSD(v)
	case float64:
		*m = MicroUSD(v)
	case []byte:
		parsed, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return err
		}
		*m = MicroUSD(parsed)
	case string:
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return err
		}
		*m = MicroUSD(parsed)
	default:
		return fmt.Errorf("cannot scan %T into MicroUSD", value)
	}
	return nil
}
