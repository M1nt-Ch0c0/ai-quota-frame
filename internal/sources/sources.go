// Package sources builds the quota and usage clients from configuration.
package sources

import (
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/cliproxy"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/config"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/googleauth"
	"github.com/M1nt-Ch0c0/ai-quota-frame/internal/usage"
)

// Clients holds the CLIProxyAPI client and, when configured, the usage
// client. Identity names the Google service account in use, or is empty when
// static management keys authenticate the requests.
type Clients struct {
	Quota    *cliproxy.Client
	Usage    *usage.Client
	Identity string
}

// New follows the explicit source authentication mode. Legacy callers that
// construct Config directly retain the old service-account behavior when no
// mode has been specified; FromEnv resolves or rejects ambiguous settings.
func New(c config.Config) (Clients, error) {
	var result Clients
	if c.SourceAuthMode == "google-id-token" || (c.SourceAuthMode == "" && c.GoogleServiceAccountFile != "") {
		token, err := googleauth.NewIDTokenSource(c.GoogleServiceAccountFile, c.GoogleIDTokenAudience, c.RequestTimeout)
		if err != nil {
			return Clients{}, err
		}
		result.Identity = token.Email()
		if result.Quota, err = cliproxy.NewClientWithBearer(c.CLIProxyBaseURL, token.Token, c.RequestTimeout); err != nil {
			return Clients{}, err
		}
		if c.CPAMPBaseURL != "" {
			if result.Usage, err = usage.NewClientWithBearer(c.CPAMPBaseURL, token.Token, c.RequestTimeout, c.Location); err != nil {
				return Clients{}, err
			}
		}
		return result, nil
	}
	var err error
	if result.Quota, err = cliproxy.NewClient(c.CLIProxyBaseURL, c.ManagementKey, c.RequestTimeout); err != nil {
		return Clients{}, err
	}
	if c.CPAMPBaseURL != "" {
		if result.Usage, err = usage.NewClient(c.CPAMPBaseURL, c.CPAMPAdminKey, c.RequestTimeout, c.Location); err != nil {
			return Clients{}, err
		}
	}
	return result, nil
}
