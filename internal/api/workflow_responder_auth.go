package api

import (
	"crypto/subtle"
	"net/http"
)

// NewBasicWorkflowResponderAuthenticator builds the fail-closed responder
// authenticator used after the server's ordinary Basic Auth middleware. Both
// configured values are required so callback/approval endpoints cannot become
// anonymously writable in Nanite's unauthenticated local-development mode.
func NewBasicWorkflowResponderAuthenticator(expectedUser, expectedPassword string) func(*http.Request) (string, bool) {
	return func(request *http.Request) (string, bool) {
		if request == nil || expectedUser == "" || expectedPassword == "" {
			return "", false
		}
		user, password, ok := request.BasicAuth()
		if !ok {
			return "", false
		}
		userMatches := subtle.ConstantTimeCompare([]byte(user), []byte(expectedUser)) == 1
		passwordMatches := subtle.ConstantTimeCompare([]byte(password), []byte(expectedPassword)) == 1
		if !userMatches || !passwordMatches {
			return "", false
		}
		return user, true
	}
}
