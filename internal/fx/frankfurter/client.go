// Package frankfurter reads foreign exchange rates from Frankfurter.
package frankfurter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
	"github.com/shopspring/decimal"
)

// Client fetches exchange rates from the Frankfurter API.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient returns a Frankfurter client that sends requests to baseURL.
func NewClient(httpClient *http.Client, baseURL string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

type rateResponse struct {
	Date  string      `json:"date"`
	Base  string      `json:"base"`
	Quote string      `json:"quote"`
	Rate  json.Number `json:"rate"`
}

// Rates returns exchange rates for currency in baseCurrency from from through to.
func (c *Client) Rates(ctx context.Context, currency, baseCurrency string, from, to time.Time) ([]model.FXRate, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("invalid request range: to %s is before from %s", to.Format(time.DateOnly), from.Format(time.DateOnly))
	}

	currency = model.NormalizeCurrency(currency)
	baseCurrency = model.NormalizeCurrency(baseCurrency)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v2/rates", nil)
	if err != nil {
		return nil, fmt.Errorf("create Frankfurter rates request: %w", err)
	}
	query := req.URL.Query()
	query.Set("base", currency)
	query.Set("quotes", baseCurrency)
	query.Set("from", from.Format(time.DateOnly))
	query.Set("to", to.Format(time.DateOnly))
	req.URL.RawQuery = query.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Frankfurter rates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch Frankfurter rates: unexpected response status %s", resp.Status)
	}

	var response []rateResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode Frankfurter rates response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode Frankfurter rates response: unexpected extra JSON value")
		}
		return nil, fmt.Errorf("decode Frankfurter rates response: %w", err)
	}

	rates := make([]model.FXRate, 0, len(response))
	for _, row := range response {
		date, err := time.Parse(time.DateOnly, row.Date)
		if err != nil {
			return nil, fmt.Errorf("parse Frankfurter response date %q: %w", row.Date, err)
		}

		responseCurrency := model.NormalizeCurrency(row.Base)
		responseBaseCurrency := model.NormalizeCurrency(row.Quote)
		if responseCurrency != currency || responseBaseCurrency != baseCurrency {
			return nil, fmt.Errorf(
				"validate Frankfurter response pair: got %s/%s, want %s/%s",
				responseCurrency,
				responseBaseCurrency,
				currency,
				baseCurrency,
			)
		}

		rate, err := decimal.NewFromString(row.Rate.String())
		if err != nil {
			return nil, fmt.Errorf("parse Frankfurter response rate %q: %w", row.Rate, err)
		}
		if !rate.IsPositive() {
			return nil, fmt.Errorf("validate Frankfurter response rate %s: must be positive", rate)
		}

		rates = append(rates, model.FXRate{
			Date:         date,
			Currency:     responseCurrency,
			BaseCurrency: responseBaseCurrency,
			Rate:         rate,
		})
	}

	return rates, nil
}
