package service

import (
	"slices"
)

// PlazaCapabilities is a public allowlist. Never serialize account metadata or
// a Codex manifest directly: those can contain private routing/tool settings.
type PlazaCapabilities struct {
	Source           string   `json:"source"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
	Reasoning        *bool    `json:"reasoning,omitempty"`
	ContextWindow    int64    `json:"context_window,omitempty"`
	MaxOutputTokens  int64    `json:"max_output_tokens,omitempty"`
}

func plazaAccountCapabilities(model string, accounts []*Account, metadata map[*Account]map[string]UpstreamModelMetadata) (*PlazaCapabilities, []string) {
	var candidates []UpstreamModelMetadata
	var protocols []string
	for _, account := range accounts {
		upstream := account.GetMappedModel(model)
		entry := metadata[account][upstream]
		candidates = append(candidates, entry) // Missing metadata participates as unknown.
		if account.HasNewAPIPlugin() {
			if account.SupportsNewAPIProviderModel(newAPIProviderChat, model) && !slices.Contains(protocols, "openai_chat") {
				protocols = append(protocols, "openai_chat")
			}
			if account.SupportsNewAPIProviderModel(newAPIProviderResponses, model) && !slices.Contains(protocols, "openai_responses") {
				protocols = append(protocols, "openai_responses")
			}
			if account.SupportsNewAPIProviderModel(newAPIProviderEmbeddings, model) && !slices.Contains(protocols, "openai_embeddings") {
				protocols = append(protocols, "openai_embeddings")
			}
			if (account.SupportsNewAPIProviderModel(newAPIProviderImagesGenerations, model) || account.SupportsNewAPIProviderModel(newAPIProviderImagesEdits, model)) && !slices.Contains(protocols, "openai_images") {
				protocols = append(protocols, "openai_images")
			}
		}
	}
	if len(candidates) == 0 {
		return nil, protocols
	}
	// The existing manifest intersection keeps only shared modalities and the
	// minimum known limits. A missing candidate removes the respective fact.
	shared := intersectUpstreamModelMetadata(model, candidates)
	var outputLimit int64
	for i, candidate := range candidates {
		if candidate.MaxOutputTokens <= 0 {
			outputLimit = 0
			break
		}
		if i == 0 || candidate.MaxOutputTokens < outputLimit {
			outputLimit = candidate.MaxOutputTokens
		}
	}
	result := &PlazaCapabilities{Source: "catalog_intersection", InputModalities: shared.InputModalities, Reasoning: shared.Reasoning, ContextWindow: shared.ContextWindow, MaxOutputTokens: outputLimit}
	if len(result.InputModalities) == 0 && result.Reasoning == nil && result.ContextWindow == 0 && result.MaxOutputTokens == 0 {
		return nil, protocols
	}
	return result, protocols
}
