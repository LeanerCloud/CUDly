package pricing

// RetailPriceItem is the canonical item shape for the Azure Retail Prices API
// (https://prices.azure.com/api/retail/prices). It is the union of all
// fields used by the service clients in this repository; services that only
// need a subset can safely ignore the extra fields; they will decode to their
// zero value.
//
// Keeping a single named type here means every service uses the same
// pricing.FetchAll[pricing.RetailPriceItem] call and the same item accessors,
// so a field rename or addition only requires a single edit.
type RetailPriceItem struct {
	CurrencyCode    string  `json:"currencyCode"`
	ArmRegionName   string  `json:"armRegionName"`
	Location        string  `json:"location"`
	ProductName     string  `json:"productName"`
	ServiceName     string  `json:"serviceName"`
	ArmSKUName      string  `json:"armSkuName"`
	SKUName         string  `json:"skuName"`
	MeterName       string  `json:"meterName"`
	UnitOfMeasure   string  `json:"unitOfMeasure"`
	ReservationTerm string  `json:"reservationTerm"`
	Type            string  `json:"type"`
	RetailPrice     float64 `json:"retailPrice"`
	UnitPrice       float64 `json:"unitPrice"`
}
