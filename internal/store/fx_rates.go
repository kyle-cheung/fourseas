package store

import (
	"context"
	"fmt"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

const upsertFXRateSQL = `
INSERT INTO fx_rates (date, currency, base_currency, rate)
VALUES (?, ?, ?, ?)
ON CONFLICT (date, currency, base_currency) DO UPDATE SET
	rate = excluded.rate
`

// RequiredFXCurrencies returns each non-base transaction currency and its
// oldest transaction date.
func (s *Store) RequiredFXCurrencies(ctx context.Context, baseCurrency string) ([]model.FXCurrency, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT currency, min(date)
		FROM transactions
		WHERE currency IS NOT NULL AND currency <> '' AND currency <> ?
		GROUP BY currency
		ORDER BY currency`, model.NormalizeCurrency(baseCurrency))
	if err != nil {
		return nil, fmt.Errorf("query required FX currencies: %w", err)
	}
	defer rows.Close()

	var currencies []model.FXCurrency
	for rows.Next() {
		var currency model.FXCurrency
		if err := rows.Scan(&currency.Currency, &currency.OldestTransactionDate); err != nil {
			return nil, fmt.Errorf("scan required FX currency: %w", err)
		}
		currencies = append(currencies, currency)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read required FX currencies: %w", err)
	}
	return currencies, nil
}

// UpsertFXRates writes rates, replacing a rate for the same date and pair.
func (s *Store) UpsertFXRates(ctx context.Context, rates []model.FXRate) error {
	return s.inTx(ctx, func(dbtx execer) error {
		if len(rates) == 0 {
			return nil
		}

		stmt, err := dbtx.PrepareContext(ctx, upsertFXRateSQL)
		if err != nil {
			return fmt.Errorf("prepare upsert FX rates: %w", err)
		}
		defer stmt.Close()

		for _, rate := range rates {
			currency := model.NormalizeCurrency(rate.Currency)
			baseCurrency := model.NormalizeCurrency(rate.BaseCurrency)
			if _, err := stmt.ExecContext(ctx, rate.Date, currency, baseCurrency, decimalArg(rate.Rate)); err != nil {
				return fmt.Errorf("upsert FX rate for %s: %w", rate.Date.Format("2006-01-02"), err)
			}
		}
		return nil
	})
}
