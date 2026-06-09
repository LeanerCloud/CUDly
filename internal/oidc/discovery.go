package oidc

// Discovery is the subset of the OpenID Connect Discovery document
// (RFC 8414) that Azure AD actually consults when validating a
// federated identity credential client_assertion.
//
// CUDly is not a full OIDC provider — it never issues ID tokens and
// never runs the authorization or token endpoints. It only publishes
// enough of a discovery document for Azure AD to locate the JWKS.
type Discovery struct {
	Issuer                           string   `json:"issuer"`
	JWKSURI                          string   `json:"jwks_uri"`
	ResponseTypesSupported           []string `json:"response_types_supported"`
	SubjectTypesSupported            []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
}

// BuildDiscovery returns the discovery document for the given issuer URL.
// issuer must match exactly what the Azure AD federated identity
// credential is configured with (trailing slashes included or excluded
// consistently), otherwise token validation fails with AADSTS700213.
func BuildDiscovery(issuer string) Discovery {
	return Discovery{
		Issuer:                           issuer,
		JWKSURI:                          issuer + "/.well-known/jwks.json",
		ResponseTypesSupported:           []string{"id_token"},
		SubjectTypesSupported:            []string{"public"},
		IDTokenSigningAlgValuesSupported: []string{Algorithm},
	}
}
