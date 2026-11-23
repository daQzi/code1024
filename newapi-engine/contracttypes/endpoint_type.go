// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) QuantumNous and contributors.
// Adapted from QuantumNous/new-api at 48f9df96ea7c68eae04a0f3687869f232f48929f.
// See ../NOTICE.md for provenance and local adaptations.

package types

// EndpointType identifies a downstream API surface. Moved from constant so
// the conversion kit (dto/relayconvert) has no host imports; constant keeps
// aliases for host code.
type EndpointType string

// Shared RelayKit policy values used by the channel route contract.
type ConversionLossPolicy string

const (
	ConversionLossPolicyAllow  ConversionLossPolicy = "allow"
	ConversionLossPolicySafe   ConversionLossPolicy = "safe"
	ConversionLossPolicyStrict ConversionLossPolicy = "strict"
)

const (
	EndpointTypeOpenAI                EndpointType = "openai"
	EndpointTypeOpenAIResponse        EndpointType = "openai-response"
	EndpointTypeOpenAIResponseCompact EndpointType = "openai-response-compact"
	EndpointTypeOpenAIAlphaSearch     EndpointType = "openai-alpha-search"
	EndpointTypeDeepSeekToolsSearch   EndpointType = "deepseek-tools-search"
	EndpointTypeDeepSeekToolsFetch    EndpointType = "deepseek-tools-fetch"
	EndpointTypeAnthropic             EndpointType = "anthropic"
	EndpointTypeGemini                EndpointType = "gemini"
	EndpointTypeJinaRerank            EndpointType = "jina-rerank"
	EndpointTypeImageGeneration       EndpointType = "image-generation"
	EndpointTypeEmbeddings            EndpointType = "embeddings"
	EndpointTypeOpenAIVideo           EndpointType = "openai-video"
)

// Finish reasons shared by the OpenAI-compatible response formats.
// Declared as vars (not consts) because converter code takes their address
// for *string finish-reason fields.
var (
	FinishReasonStop          = "stop"
	FinishReasonToolCalls     = "tool_calls"
	FinishReasonLength        = "length"
	FinishReasonFunctionCall  = "function_call"
	FinishReasonContentFilter = "content_filter"
)
