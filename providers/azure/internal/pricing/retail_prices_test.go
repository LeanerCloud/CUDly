package pricing

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeItem is the per-test item shape. Real callers define their own
// named struct in their own package; tests use this minimal shape to
// prove FetchAll decodes and merges across pages.
type fakeItem struct {
	Name string `json:"name"`
}

// fakeHTTPClient is a scripted HTTP client — Do returns a fixed response
// or error per URL. Keeping the fake inside this file avoids depending on
// testify/mock for a simple behaviour contract.
type fakeHTTPClient struct {
	responses map[string]*http.Response
	errors    map[string]error
	calls     []*http.Request
	beforeDo  func(*http.Request)
}

func newFakeHTTPClient() *fakeHTTPClient {
	return &fakeHTTPClient{
		responses: map[string]*http.Response{},
		errors:    map[string]error{},
	}
}

func (f *fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	f.calls = append(f.calls, req)
	if f.beforeDo != nil {
		f.beforeDo(req)
	}
	url := req.URL.String()
	if err, ok := f.errors[url]; ok {
		return nil, err
	}
	if resp, ok := f.responses[url]; ok {
		return resp, nil
	}
	return nil, errors.New("fakeHTTPClient: no scripted response for " + url)
}

func okJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

// TestFetchAll_MergesPages pins the multi-page walk: page 1 has a non-
// empty NextPageLink, page 2 has an empty NextPageLink, all items are
// merged into the returned slice in order.
func TestFetchAll_MergesPages(t *testing.T) {
	client := newFakeHTTPClient()
	client.responses["https://prices.example/page1"] = okJSONResponse(
		`{"Items":[{"name":"a"}],"NextPageLink":"https://prices.example/page2"}`,
	)
	client.responses["https://prices.example/page2"] = okJSONResponse(
		`{"Items":[{"name":"b"},{"name":"c"}],"NextPageLink":""}`,
	)

	items, err := FetchAll[fakeItem](context.Background(), client, "https://prices.example/page1", DefaultPageTimeout, DefaultMaxPages)
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, "a", items[0].Name)
	assert.Equal(t, "b", items[1].Name)
	assert.Equal(t, "c", items[2].Name)
}

// TestFetchAll_RejectsSelfReferentialNextPageLink guards against the
// server-bug case where NextPageLink points at the URL that just produced
// it. Without the seen-URL set the walker would loop forever.
func TestFetchAll_RejectsSelfReferentialNextPageLink(t *testing.T) {
	client := newFakeHTTPClient()
	client.responses["https://prices.example/loop"] = okJSONResponse(
		`{"Items":[],"NextPageLink":"https://prices.example/loop"}`,
	)

	_, err := FetchAll[fakeItem](context.Background(), client, "https://prices.example/loop", DefaultPageTimeout, DefaultMaxPages)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "self-referential")
}

// TestFetchAll_HonoursMaxPagesCap covers the defensive cap: if the server
// returns a genuinely unbounded chain of fresh NextPageLinks, the walker
// must stop after maxPages instead of running forever.
func TestFetchAll_HonoursMaxPagesCap(t *testing.T) {
	client := newFakeHTTPClient()
	for i := 0; i < 10; i++ {
		url := "https://prices.example/page" + string(rune('a'+i))
		next := ""
		if i < 9 {
			next = "https://prices.example/page" + string(rune('a'+i+1))
		}
		client.responses[url] = okJSONResponse(
			`{"Items":[{"name":"` + string(rune('a'+i)) + `"}],"NextPageLink":"` + next + `"}`,
		)
	}

	// Cap at 3 — walker should read pages a, b, c only.
	items, err := FetchAll[fakeItem](context.Background(), client, "https://prices.example/pagea", DefaultPageTimeout, 3)
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, "a", items[0].Name)
	assert.Equal(t, "c", items[2].Name)
	assert.Len(t, client.calls, 3, "walker must not fetch beyond maxPages")
}

// TestFetchAll_PerPageTimeout proves the per-page timeout is applied
// independently of the caller's outer ctx: the request context carries
// a deadline, and a page failure does NOT cancel the outer ctx.
func TestFetchAll_PerPageTimeout(t *testing.T) {
	client := newFakeHTTPClient()
	client.responses["https://prices.example/page1"] = okJSONResponse(
		`{"Items":[{"name":"a"}],"NextPageLink":"https://prices.example/page2"}`,
	)
	client.errors["https://prices.example/page2"] = context.DeadlineExceeded

	client.beforeDo = func(req *http.Request) {
		if req.URL.String() == "https://prices.example/page2" {
			if _, ok := req.Context().Deadline(); !ok {
				t.Errorf("page 2 request has no deadline — per-page timeout not applied")
			}
		}
	}

	outerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := FetchAll[fakeItem](outerCtx, client, "https://prices.example/page1", 10*time.Second, DefaultMaxPages)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "page 1")
	assert.Contains(t, err.Error(), "timeout")
	assert.NoError(t, outerCtx.Err(), "outer ctx must not be cancelled by a per-page timeout")
}

// TestFetchAll_RejectsNonOKStatus covers the HTTP-error path: any non-200
// surfaces as a wrapped error carrying the body (for diagnosis) and the
// page index.
func TestFetchAll_RejectsNonOKStatus(t *testing.T) {
	client := newFakeHTTPClient()
	client.responses["https://prices.example/page1"] = &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(bytes.NewBufferString(`{"error":"boom"}`)),
		Header:     make(http.Header),
	}

	_, err := FetchAll[fakeItem](context.Background(), client, "https://prices.example/page1", DefaultPageTimeout, DefaultMaxPages)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
	assert.Contains(t, err.Error(), "page 0")
	assert.Contains(t, err.Error(), "boom")
}

// TestFetchAll_ZeroMaxPages guards against an obvious caller bug.
func TestFetchAll_ZeroMaxPages(t *testing.T) {
	client := newFakeHTTPClient()
	_, err := FetchAll[fakeItem](context.Background(), client, "https://prices.example/page1", DefaultPageTimeout, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxPages must be > 0")
}
