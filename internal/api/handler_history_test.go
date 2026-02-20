package api

import (
	"context"
	"testing"

	"github.com/LeanerCloud/CUDly/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_getHistory(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	history := []config.PurchaseHistoryRecord{
		{AccountID: "123456789012", PurchaseID: "purchase-1", UpfrontCost: 100.0, EstimatedSavings: 10.0},
	}

	mockStore.On("GetPurchaseHistory", ctx, "123456789012", 100).Return(history, nil)

	handler := &Handler{config: mockStore}

	params := map[string]string{
		"account_id": "123456789012",
	}

	result, err := handler.getHistory(ctx, params)
	require.NoError(t, err)

	historyResp := result.(HistoryResponse)
	assert.Len(t, historyResp.Purchases, 1)
	assert.Equal(t, 1, historyResp.Summary.TotalPurchases)
	assert.Equal(t, 100.0, historyResp.Summary.TotalUpfront)
	assert.Equal(t, 10.0, historyResp.Summary.TotalMonthlySavings)
	assert.Equal(t, 120.0, historyResp.Summary.TotalAnnualSavings)
}

func TestHandler_getHistory_AllAccounts(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	history := []config.PurchaseHistoryRecord{
		{AccountID: "111111111111", PurchaseID: "purchase-1", UpfrontCost: 100.0, EstimatedSavings: 10.0},
		{AccountID: "222222222222", PurchaseID: "purchase-2", UpfrontCost: 200.0, EstimatedSavings: 20.0},
	}

	mockStore.On("GetAllPurchaseHistory", ctx, 100).Return(history, nil)

	handler := &Handler{config: mockStore}

	params := map[string]string{}

	result, err := handler.getHistory(ctx, params)
	require.NoError(t, err)

	historyResp := result.(HistoryResponse)
	assert.Len(t, historyResp.Purchases, 2)
	assert.Equal(t, 2, historyResp.Summary.TotalPurchases)
	assert.Equal(t, 300.0, historyResp.Summary.TotalUpfront)
	assert.Equal(t, 30.0, historyResp.Summary.TotalMonthlySavings)
}

func TestHandler_getHistory_CustomLimit(t *testing.T) {
	ctx := context.Background()
	mockStore := new(MockConfigStore)

	mockStore.On("GetAllPurchaseHistory", ctx, 50).Return([]config.PurchaseHistoryRecord{}, nil)

	handler := &Handler{config: mockStore}

	params := map[string]string{
		"limit": "50",
	}

	_, err := handler.getHistory(ctx, params)
	require.NoError(t, err)
}
