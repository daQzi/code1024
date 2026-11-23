// SPDX-License-Identifier: AGPL-3.0-or-later
// Grounding-query union follows the pinned New API's geminiGroundingCounter
// in relay/channel/gemini/relay-gemini.go, with bounded hashed identities.
package provider

import (
	"crypto/sha256"
	"errors"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	GoogleSearchTool               = "google_search"
	GoogleSearchGroundedPromptTool = "google_search_grounded_prompt"
)

// Gemini provides search queries, not an authoritative billed-call counter.
// Keep both baseline metrics; the host's explicit per-model prices decide
// which metric is charged. Never infer a price regime from a model name.
func (p *ProtocolTools) observeGeminiGrounding(root gjson.Result) error {
	candidates, err := vendorToolPath(root, "candidates")
	if err != nil || !candidates.Exists() || candidates.Type == gjson.Null {
		return err
	}
	if !candidates.IsArray() {
		return errors.New("invalid provider Gemini candidates")
	}
	added := make(map[[32]byte]struct{})
	for _, candidate := range candidates.Array() {
		metadata, err := vendorToolPath(candidate, "groundingMetadata")
		if err != nil {
			return err
		}
		for _, key := range []string{"webSearchQueries", "imageSearchQueries"} {
			queries, err := vendorToolPath(metadata, key)
			if err != nil {
				return err
			}
			if !queries.Exists() || queries.Type == gjson.Null {
				continue
			}
			if !queries.IsArray() {
				return errors.New("invalid provider Gemini grounding queries")
			}
			for _, value := range queries.Array() {
				if value.Type != gjson.String {
					return errors.New("invalid provider Gemini grounding query")
				}
				query := strings.TrimSpace(value.String())
				if query == "" {
					continue
				}
				id := sha256.Sum256([]byte(query))
				if _, exists := p.grounding[id]; exists {
					continue
				}
				added[id] = struct{}{}
				if len(p.grounding)+len(added) > MaxToolCalls {
					return errors.New("provider Gemini grounding queries exceed limit")
				}
			}
		}
	}
	if len(added) == 0 {
		return nil
	}
	if err := p.replaceVendorCounts(map[string]int{
		GoogleSearchTool: len(p.grounding) + len(added), GoogleSearchGroundedPromptTool: 1,
	}); err != nil {
		return err
	}
	if p.grounding == nil {
		p.grounding = added
	} else {
		for id := range added {
			p.grounding[id] = struct{}{}
		}
	}
	return nil
}
