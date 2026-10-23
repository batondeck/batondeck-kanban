package oauth

import "net/http"

const asMetadataPath = "/.well-known/oauth-authorization-server"

// metadata is the RFC 8414 document. There is no jwks_uri: the tokens are opaque and only this
// binary reads them. Public clients only (token endpoint auth "none"), PKCE S256 only.
func (s *Server) metadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.issuer,
		"authorization_endpoint":                s.issuer + "/authorize",
		"token_endpoint":                        s.issuer + "/token",
		"registration_endpoint":                 s.issuer + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{grantCode, grantRefresh},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      roles(),
	})
}
