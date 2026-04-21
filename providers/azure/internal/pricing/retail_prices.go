// Package pricing provides a shared NextPageLink-driven walker for the
// Azure Retail Prices API, used by every service client (compute,
// database, cache, cosmosdb). The per-service client packages retain
// their service-specific Items types as the T type parameter; only the
// envelope (`Page[T]`) and the walker (`FetchAll[T]`) live here.
//
// Without this package every service client carries a near-identical
// copy of the pagination loop — the same seen-URL guard, the same
// max-pages cap, the same per-page timeout, the same error wording.
// Centralising those invariants means a bug in one (e.g. a missing
// timeout) can't diverge across services.
package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPClient is the minimal surface FetchAll needs. Declared here (rather
// than imported from each service's client package) so the shared package
// has no upstream dependency on the per-service types.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Page is the envelope shared across every Azure Retail Prices response.
// T is the per-service item shape; callers define their own named item
// type in their own package so the JSON decode produces values the caller
// already knows how to read.
type Page[T any] struct {
	Items        []T    `json:"Items"`
	NextPageLink string `json:"NextPageLink"`
	Count        int    `json:"Count"`
}

// DefaultPageTimeout bounds each individual page GET. Callers that want a
// different per-page budget can pass their own value to FetchAll; most
// service clients use this default.
const DefaultPageTimeout = 10 * time.Second

// DefaultMaxPages caps the NextPageLink loop. The Azure Retail Prices API
// paginates at 100 items per page, so 50 pages is 5000 items — more than
// any realistic SKU/region/term query. The cap is purely a defence against
// a server bug returning a NextPageLink that never empties.
const DefaultMaxPages = 50

// FetchAll walks the Retail Prices API starting at initialURL, appending
// every page's Items into the returned slice. Enforces three invariants:
//
//   - a per-page timeout (pageTimeout) that's independent of the caller's
//     ctx, so one slow page can't consume the caller's whole budget;
//   - a max-pages cap (maxPages) against a server bug returning an
//     infinite NextPageLink chain;
//   - a seen-URL guard against a self-referential NextPageLink.
//
// ctx's cancellation still propagates via context.WithTimeout(ctx, ...),
// so caller-initiated cancel() still aborts the walk — the per-page
// timeout only scopes the deadline.
func FetchAll[T any](ctx context.Context, httpClient HTTPClient, initialURL string, pageTimeout time.Duration, maxPages int) ([]T, error) {
	if maxPages <= 0 {
		return nil, fmt.Errorf("pricing.FetchAll: maxPages must be > 0, got %d", maxPages)
	}

	all := make([]T, 0)
	nextURL := initialURL
	seen := map[string]struct{}{}

	for pageIdx := 0; pageIdx < maxPages && nextURL != ""; pageIdx++ {
		if _, ok := seen[nextURL]; ok {
			return nil, fmt.Errorf("pricing API returned a self-referential NextPageLink (page %d)", pageIdx)
		}
		seen[nextURL] = struct{}{}

		page, err := fetchOnePage[T](ctx, httpClient, nextURL, pageIdx, pageTimeout)
		if err != nil {
			return nil, err
		}

		all = append(all, page.Items...)
		nextURL = page.NextPageLink
	}

	return all, nil
}

// fetchOnePage issues one GET with a bounded timeout and decodes the
// response. pageCtx inherits cancellation from the caller's ctx but the
// per-page timeout is independent. defer cancel() inside a function body
// correctly releases each page's context on every return path — using
// defer inside a loop body would leak a context per iteration.
func fetchOnePage[T any](ctx context.Context, httpClient HTTPClient, pageURL string, pageIdx int, pageTimeout time.Duration) (*Page[T], error) {
	pageCtx, cancel := context.WithTimeout(ctx, pageTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(pageCtx, "GET", pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request (page %d): %w", pageIdx, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call pricing API (page %d, timeout %s): %w", pageIdx, pageTimeout, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pricing API returned status %d (page %d): %s", resp.StatusCode, pageIdx, string(body))
	}

	var page Page[T]
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("failed to decode pricing response (page %d): %w", pageIdx, err)
	}
	return &page, nil
}
