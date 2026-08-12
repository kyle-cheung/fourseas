package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/shopspring/decimal"
)

// Money crosses the driver as text in both directions. The driver does not
// accept a decimal as a parameter, and DuckDB casts the text to the column
// type, so no float ever touches the value.

// decimalArg turns a decimal into an insert parameter.
func decimalArg(d decimal.Decimal) any { return d.String() }

// nullDecimalArg turns an optional decimal into an insert parameter.
func nullDecimalArg(d decimal.NullDecimal) any {
	if !d.Valid {
		return nil
	}
	return d.Decimal.String()
}

// textArg writes an empty string as NULL, so that "not set" is one value and
// not two.
func textArg(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// timeArg writes a missing time as NULL.
func timeArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// toDecimal reads a value scanned from a DECIMAL column.
func toDecimal(v any) (decimal.Decimal, error) {
	switch d := v.(type) {
	case nil:
		return decimal.Decimal{}, nil
	case duckdb.Decimal:
		if d.Value == nil {
			return decimal.Decimal{}, nil
		}
		return decimal.NewFromBigInt(d.Value, -int32(d.Scale)), nil
	case int64:
		return decimal.NewFromInt(d), nil
	case string:
		return decimal.NewFromString(d)
	default:
		return decimal.Decimal{}, fmt.Errorf("cannot read %T as a decimal", v)
	}
}

// toNullDecimal reads a value scanned from a nullable DECIMAL column.
func toNullDecimal(v any) (decimal.NullDecimal, error) {
	if v == nil {
		return decimal.NullDecimal{}, nil
	}
	d, err := toDecimal(v)
	if err != nil {
		return decimal.NullDecimal{}, err
	}
	return decimal.NullDecimal{Decimal: d, Valid: true}, nil
}

// text reads a nullable VARCHAR as a plain string, where NULL is empty.
func text(s sql.NullString) string { return s.String }

// timePtr reads a nullable date or timestamp.
func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	when := t.Time
	return &when
}
