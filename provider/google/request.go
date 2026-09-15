// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package google

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/leelsey/golm"
)

type apiFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type apiFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type apiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type apiPart struct {
	Text             string               `json:"text,omitempty"`
	Thought          bool                 `json:"thought,omitempty"`
	ThoughtSignature string               `json:"thoughtSignature,omitempty"`
	FunctionCall     *apiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *apiFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *apiInlineData       `json:"inlineData,omitempty"`
}

type apiContent struct {
	Role  string    `json:"role,omitempty"`
	Parts []apiPart `json:"parts"`
}

type apiFunctionDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type apiToolDecl struct {
	FunctionDeclarations []apiFunctionDecl `json:"functionDeclarations"`
}

type thinkingConfig struct {
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int `json:"thinkingBudget,omitempty"`
}

type genConfig struct {
	MaxOutputTokens    int             `json:"maxOutputTokens,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	ThinkingConfig     *thinkingConfig `json:"thinkingConfig,omitempty"`
	ResponseModalities []string        `json:"responseModalities,omitempty"`
}

type apiRequest struct {
	SystemInstruction *apiContent        `json:"systemInstruction,omitempty"`
	Contents          []apiContent       `json:"contents"`
	Tools             []apiToolDecl      `json:"tools,omitempty"`
	ToolConfig        *apiToolConfig     `json:"toolConfig,omitempty"`
	SafetySettings    []apiSafetySetting `json:"safetySettings,omitempty"`
	GenerationConfig  *genConfig         `json:"generationConfig,omitempty"`
}

type apiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

var safetyCategories = []string{
	"HARM_CATEGORY_HARASSMENT",
	"HARM_CATEGORY_HATE_SPEECH",
	"HARM_CATEGORY_SEXUALLY_EXPLICIT",
	"HARM_CATEGORY_DANGEROUS_CONTENT",
}

func safetySettings(level golm.SafetyLevel) []apiSafetySetting {
	var threshold string
	switch level {
	case golm.SafetyNone:
		threshold = "BLOCK_NONE"
	case golm.SafetyLowered:
		threshold = "BLOCK_ONLY_HIGH"
	default:
		return nil
	}
	out := make([]apiSafetySetting, 0, len(safetyCategories))
	for _, c := range safetyCategories {
		out = append(out, apiSafetySetting{Category: c, Threshold: threshold})
	}
	return out
}

type apiToolConfig struct {
	FunctionCallingConfig apiFunctionCallingConfig `json:"functionCallingConfig"`
}

type apiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

func buildContents(req golm.Request) []apiContent {
	idToName := map[string]string{}
	contents := make([]apiContent, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case golm.RoleSystem:
			continue
		case golm.RoleTool:
			parts := make([]apiPart, 0, len(m.Content))
			for _, c := range m.Content {
				tr, ok := c.(golm.ToolResult)
				if !ok {
					continue
				}
				resp := map[string]any{"result": tr.Text()}
				if tr.IsError {
					resp = map[string]any{"error": tr.Text()}
				}
				name := tr.Name
				if name == "" {
					name = idToName[tr.ToolUseID]
				}
				parts = append(parts, apiPart{FunctionResponse: &apiFunctionResponse{
					ID: tr.ToolUseID, Name: name, Response: resp,
				}})
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, apiContent{Role: "user", Parts: parts})
		case golm.RoleAssistant:
			parts := make([]apiPart, 0, len(m.Content))
			for _, c := range m.Content {
				switch v := c.(type) {
				case golm.Text:
					parts = append(parts, apiPart{Text: v.Text})
				case golm.Thinking:
					parts = append(parts, apiPart{Text: v.Text, Thought: true, ThoughtSignature: v.Signature})
				case golm.Plan:
					parts = append(parts, apiPart{Text: strings.Join(v.Steps, "\n")})
				case golm.ToolUse:
					idToName[v.ID] = v.Name
					args := v.Input
					if len(args) == 0 {
						args = json.RawMessage("{}")
					}
					parts = append(parts, apiPart{FunctionCall: &apiFunctionCall{ID: v.ID, Name: v.Name, Args: args}, ThoughtSignature: v.Signature})
				case golm.Image:
					if v.URL == "" && len(v.Data) > 0 {
						parts = append(parts, apiPart{InlineData: &apiInlineData{
							MimeType: v.MediaType, Data: base64.StdEncoding.EncodeToString(v.Data),
						}})
					}
				case golm.Audio:
					if len(v.Data) > 0 {
						parts = append(parts, apiPart{InlineData: &apiInlineData{
							MimeType: v.MediaType, Data: base64.StdEncoding.EncodeToString(v.Data),
						}})
					}
				}
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, apiContent{Role: "model", Parts: parts})
		default:
			parts := make([]apiPart, 0, len(m.Content))
			for _, c := range m.Content {
				switch v := c.(type) {
				case golm.Text:
					parts = append(parts, apiPart{Text: v.Text})
				case golm.Plan:
					parts = append(parts, apiPart{Text: strings.Join(v.Steps, "\n")})
				case golm.Image:
					if v.URL == "" && len(v.Data) > 0 {
						parts = append(parts, apiPart{InlineData: &apiInlineData{
							MimeType: v.MediaType, Data: base64.StdEncoding.EncodeToString(v.Data),
						}})
					}
				case golm.Audio:
					if len(v.Data) > 0 {
						parts = append(parts, apiPart{InlineData: &apiInlineData{
							MimeType: v.MediaType, Data: base64.StdEncoding.EncodeToString(v.Data),
						}})
					}
				}
			}
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, apiContent{Role: "user", Parts: parts})
		}
	}
	return contents
}

func (c *Client) buildRequest(req golm.Request) apiRequest {
	out := apiRequest{Contents: buildContents(req)}
	sys := req.System.Text()
	for _, m := range req.Messages {
		if m.Role == golm.RoleSystem {
			if t := m.Text(); t != "" {
				if sys != "" {
					sys += "\n\n"
				}
				sys += t
			}
		}
	}
	if sys != "" {
		out.SystemInstruction = &apiContent{Parts: []apiPart{{Text: sys}}}
	}
	if len(req.Tools) > 0 {
		decls := make([]apiFunctionDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, apiFunctionDecl{Name: t.Name, Description: t.Description, Parameters: t.Schema})
		}
		out.Tools = []apiToolDecl{{FunctionDeclarations: decls}}
	}
	if req.ToolChoice != "" {
		out.ToolConfig = &apiToolConfig{FunctionCallingConfig: apiFunctionCallingConfig{
			Mode:                 "ANY",
			AllowedFunctionNames: []string{req.ToolChoice},
		}}
	}
	out.SafetySettings = safetySettings(req.Safety)

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = golm.DefaultMaxTokens
	}
	gc := &genConfig{MaxOutputTokens: maxTokens}
	if req.Temperature != nil {
		gc.Temperature = req.Temperature
	}

	if req.Thinking.Mode == golm.ThinkingAuto || req.Thinking.Mode == golm.ThinkingBudget {
		tc := &thinkingConfig{IncludeThoughts: true}
		if req.Thinking.Mode == golm.ThinkingBudget && req.Thinking.Budget > 0 {
			b := req.Thinking.Budget
			tc.ThinkingBudget = &b
		}
		gc.ThinkingConfig = tc
	}
	for _, mod := range req.ResponseModalities {
		gc.ResponseModalities = append(gc.ResponseModalities, strings.ToUpper(mod))
	}
	out.GenerationConfig = gc
	return out
}
