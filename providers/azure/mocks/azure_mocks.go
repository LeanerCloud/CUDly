// Package mocks provides mock implementations of Azure SDK clients for testing
package mocks

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/consumption/armconsumption"
	"github.com/stretchr/testify/mock"
)

// MockRecommendationsPager mocks the recommendations pager
type MockRecommendationsPager struct {
	mock.Mock
	Results    []armconsumption.ReservationRecommendationClassification
	HasMore    bool
	pageCount  int
}

// More returns whether there are more pages
func (p *MockRecommendationsPager) More() bool {
	if p.pageCount == 0 {
		return p.HasMore
	}
	return false
}

// NextPage returns the next page of results
func (p *MockRecommendationsPager) NextPage(ctx context.Context) (armconsumption.ReservationRecommendationsClientListResponse, error) {
	p.pageCount++
	return armconsumption.ReservationRecommendationsClientListResponse{
		ReservationRecommendationsListResult: armconsumption.ReservationRecommendationsListResult{
			Value: p.Results,
		},
	}, nil
}

// MockReservationsDetailsPager mocks the reservations details pager
type MockReservationsDetailsPager struct {
	mock.Mock
	Results   []*armconsumption.ReservationDetail
	HasMore   bool
	pageCount int
}

// More returns whether there are more pages
func (p *MockReservationsDetailsPager) More() bool {
	if p.pageCount == 0 {
		return p.HasMore
	}
	return false
}

// NextPage returns the next page of results
func (p *MockReservationsDetailsPager) NextPage(ctx context.Context) (armconsumption.ReservationsDetailsClientListByReservationOrderResponse, error) {
	p.pageCount++
	return armconsumption.ReservationsDetailsClientListByReservationOrderResponse{
		ReservationDetailsListResult: armconsumption.ReservationDetailsListResult{
			Value: p.Results,
		},
	}, nil
}

// MockResourceSKUsPager mocks the resource SKUs pager
type MockResourceSKUsPager struct {
	mock.Mock
	Results   []*armcompute.ResourceSKU
	HasMore   bool
	pageCount int
}

// More returns whether there are more pages
func (p *MockResourceSKUsPager) More() bool {
	if p.pageCount == 0 {
		return p.HasMore
	}
	return false
}

// NextPage returns the next page of results
func (p *MockResourceSKUsPager) NextPage(ctx context.Context) (armcompute.ResourceSKUsClientListResponse, error) {
	p.pageCount++
	return armcompute.ResourceSKUsClientListResponse{
		ResourceSKUsResult: armcompute.ResourceSKUsResult{
			Value: p.Results,
		},
	}, nil
}

// MockHTTPClient mocks an HTTP client
type MockHTTPClient struct {
	mock.Mock
	ResponseBody string
	StatusCode   int
}

// Do performs the mock HTTP request
func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*http.Response), args.Error(1)
}

// CreateMockHTTPResponse creates a mock HTTP response
func CreateMockHTTPResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

// Helper functions

// StringPtr returns a pointer to a string
func StringPtr(s string) *string {
	return &s
}

// Float64Ptr returns a pointer to a float64
func Float64Ptr(f float64) *float64 {
	return &f
}

// Int32Ptr returns a pointer to an int32
func Int32Ptr(i int32) *int32 {
	return &i
}

// Int64Ptr returns a pointer to an int64
func Int64Ptr(i int64) *int64 {
	return &i
}

// BoolPtr returns a pointer to a bool
func BoolPtr(b bool) *bool {
	return &b
}

// CreateSampleResourceSKUs creates sample resource SKUs for testing
func CreateSampleResourceSKUs(region string) []*armcompute.ResourceSKU {
	resourceType := "virtualMachines"
	return []*armcompute.ResourceSKU{
		{
			Name:         StringPtr("Standard_D2s_v3"),
			ResourceType: &resourceType,
			Locations:    []*string{StringPtr(region)},
		},
		{
			Name:         StringPtr("Standard_D4s_v3"),
			ResourceType: &resourceType,
			Locations:    []*string{StringPtr(region)},
		},
		{
			Name:         StringPtr("Standard_D8s_v3"),
			ResourceType: &resourceType,
			Locations:    []*string{StringPtr(region)},
		},
	}
}

// CreateSampleReservationDetails creates sample reservation details for testing
func CreateSampleReservationDetails(subscriptionID, region string) []*armconsumption.ReservationDetail {
	skuName := "VirtualMachines/Standard_D2s_v3"
	reservationID := "reservation-123"
	return []*armconsumption.ReservationDetail{
		{
			Properties: &armconsumption.ReservationDetailProperties{
				SKUName:       &skuName,
				ReservationID: &reservationID,
			},
		},
	}
}

// CreateSampleVMPricingResponse creates a sample VM pricing response for testing
func CreateSampleVMPricingResponse() string {
	return `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 500.0,
				"unitPrice": 0.096,
				"armRegionName": "eastus",
				"productName": "Virtual Machines D Series",
				"serviceName": "Virtual Machines",
				"armSkuName": "Standard_D2s_v3",
				"reservationTerm": "1 Years",
				"type": "Reservation"
			},
			{
				"currencyCode": "USD",
				"retailPrice": 0.096,
				"unitPrice": 0.096,
				"armRegionName": "eastus",
				"productName": "Virtual Machines D Series",
				"serviceName": "Virtual Machines",
				"armSkuName": "Standard_D2s_v3",
				"type": "Consumption"
			}
		]
	}`
}

// CreateSampleSQLPricingResponse creates a sample SQL pricing response for testing
func CreateSampleSQLPricingResponse() string {
	return `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 750.0,
				"unitPrice": 750.0,
				"armRegionName": "eastus",
				"location": "US East",
				"meterName": "S0 DTUs",
				"skuName": "Standard",
				"productName": "SQL Database",
				"serviceName": "SQL Database",
				"unitOfMeasure": "1 DTU/Hour",
				"type": "Reservation",
				"armSkuName": "Standard_S0",
				"reservationTerm": "1 Year"
			}
		],
		"NextPageLink": "",
		"Count": 1
	}`
}

// CreateSampleRedisPricingResponse creates a sample Redis pricing response for testing
func CreateSampleRedisPricingResponse() string {
	return `{
		"Items": [
			{
				"currencyCode": "USD",
				"retailPrice": 350.0,
				"unitPrice": 350.0,
				"armRegionName": "eastus",
				"productName": "Azure Cache for Redis",
				"serviceName": "Azure Cache for Redis",
				"skuName": "Premium P1",
				"reservationTerm": "1 Year",
				"type": "Reservation"
			}
		]
	}`
}
