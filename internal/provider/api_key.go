package provider

// APIKeySetter is implemented by providers that accept an API key override.
type APIKeySetter interface {
	SetAPIKey(key string)
}

// SetAPIKey on Anthropic provider.
func (a *Anthropic) SetAPIKey(key string) { a.apiKey = key }

// SetAPIKey on OpenAI provider.
func (o *OpenAI) SetAPIKey(key string) { o.apiKey = key }

// SetAPIKey on Gemini provider.
func (g *Gemini) SetAPIKey(key string) { g.apiKey = key }

// SetAPIKey on Mistral provider.
func (m *Mistral) SetAPIKey(key string) { m.apiKey = key }

// SetAPIKey on AzureOpenAI provider.
func (az *AzureOpenAI) SetAPIKey(key string) { az.apiKey = key }
