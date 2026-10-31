package apiauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	auth "github.com/hollis-labs/libs/security/auth"
)

// Environment variables that enable the Tether service-token verifier. With
// none of them set the verifier is off and authentication is unchanged.
const (
	EnvTetherIssuer       = "NANITE_TETHER_ISSUER"             // Tether base URL; derives the JWKS and deny-list URLs
	EnvTetherIssuerClaim  = "NANITE_TETHER_ISSUER_CLAIM"       // required iss claim (default "tether")
	EnvTetherJWKSURL      = "NANITE_TETHER_JWKS_URL"           // override of <issuer>/.well-known/jwks.json
	EnvTetherDenyList     = "NANITE_TETHER_DENYLIST_URL"       // override of <issuer>/auth/denylist
	EnvTetherBootstrap    = "NANITE_TETHER_BOOTSTRAP_KEY_FILE" // JWKS JSON or PEM public key used until the first fetch
	EnvTetherAudience     = "NANITE_TETHER_AUDIENCE"           // required "aud" (default "nanite")
	defaultTetherAudience = "nanite"
)

// ServiceTokenVerifier authenticates a bearer minted by another app's issuer
// (Tether service tokens). It is an additional way in alongside credentials;
// it never replaces them.
type ServiceTokenVerifier interface {
	VerifyServiceToken(ctx context.Context, token string) (Principal, error)
}

// TetherConfig configures the Tether service-token verifier.
type TetherConfig struct {
	// Issuer is the Tether base URL; IssuerClaim is the required "iss" claim
	// (Tether stamps "tether", which is the default).
	Issuer, IssuerClaim, JWKSURL, DenyListURL, BootstrapKeyFile, Audience string
}

// TetherConfigFromEnv reads the NANITE_TETHER_* verifier settings.
func TetherConfigFromEnv() TetherConfig {
	g := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	return TetherConfig{
		Issuer: g(EnvTetherIssuer), IssuerClaim: g(EnvTetherIssuerClaim), JWKSURL: g(EnvTetherJWKSURL), DenyListURL: g(EnvTetherDenyList),
		BootstrapKeyFile: g(EnvTetherBootstrap), Audience: g(EnvTetherAudience),
	}
}

// Enabled reports whether any verifier setting is present.
func (c TetherConfig) Enabled() bool {
	return c.Issuer != "" || c.JWKSURL != "" || c.BootstrapKeyFile != ""
}

// TetherVerifier verifies Tether-issued Ed25519 JWTs offline against the
// issuer's JWKS, honouring the issuer's revocation deny-list.
type TetherVerifier struct{ jv *auth.JWTVerifier }

// NewTetherVerifier builds the verifier and starts its JWKS refresher and
// deny-list poller (both stop with ctx). It returns (nil, nil) when cfg is not
// enabled, so the default behaviour is unchanged.
func NewTetherVerifier(ctx context.Context, cfg TetherConfig) (*TetherVerifier, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	issuer := strings.TrimRight(cfg.Issuer, "/")
	if issuer != "" {
		if u, err := url.Parse(issuer); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("%s must be an http(s) URL", EnvTetherIssuer)
		}
	}
	jwks := cfg.JWKSURL
	if jwks == "" && issuer != "" {
		jwks = issuer + "/.well-known/jwks.json"
	}
	if jwks == "" && cfg.BootstrapKeyFile == "" {
		return nil, errors.New("tether verifier needs an issuer, a JWKS URL or a bootstrap key file")
	}
	aud := cfg.Audience
	if aud == "" {
		aud = defaultTetherAudience
	}
	denyURL := cfg.DenyListURL
	if denyURL == "" && issuer != "" {
		denyURL = issuer + "/auth/denylist"
	}
	claim := cfg.IssuerClaim
	if claim == "" {
		claim = "tether"
	}
	jc := auth.JWTConfig{Issuer: claim, Audience: aud, JWKSURL: jwks, BootstrapKeyFile: cfg.BootstrapKeyFile}
	var dl *auth.DenyList
	if denyURL != "" {
		dl = auth.NewDenyList()
		jc.DenyList = dl
	}
	jv, err := auth.NewJWTVerifier(jc)
	if err != nil {
		return nil, fmt.Errorf("tether verifier: %w", err)
	}
	go jv.Run(ctx)
	if dl != nil {
		go (&auth.DenyListPoller{URL: denyURL, List: dl}).Run(ctx)
	}
	return &TetherVerifier{jv: jv}, nil
}

// serviceScopes is what a valid Tether service token is granted: the same
// surface as a client credential holding read+operate+tools. Never admin or
// maintain.
func serviceScopes() []Scope { return []Scope{ScopeRead, ScopeOperate, ScopeTools} }

// VerifyServiceToken validates the token (signature, expiry, issuer, audience,
// deny-list) and returns the principal it maps to.
func (v *TetherVerifier) VerifyServiceToken(ctx context.Context, token string) (Principal, error) {
	p, err := v.jv.Verify(ctx, token)
	if err != nil {
		return Principal{}, err
	}
	if p.ID == "" {
		return Principal{}, auth.ErrInvalidToken
	}
	label := p.Display
	if label == "" {
		label = p.ID
	}
	return Principal{CredentialID: "tether:" + p.ID, Kind: KindClient, Label: "tether/" + label, Scopes: serviceScopes()}, nil
}
