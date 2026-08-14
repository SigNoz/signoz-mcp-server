package tools

import (
	"fmt"

	mcp "github.com/SigNoz/signoz-mcp-server/internal/mcpcontract"
)

type registrationKind string

type registrationKey struct {
	server *mcp.Server
	kind   registrationKind
	name   string
}

const (
	registrationTool             registrationKind = "tool"
	registrationResource         registrationKind = "resource"
	registrationResourceTemplate registrationKind = "resource template"
	registrationPrompt           registrationKind = "prompt"
)

func (h *Handler) claimRegistration(s *mcp.Server, kind registrationKind, key string) {
	if s == nil {
		panic(fmt.Sprintf("register %s %q on nil MCP server", kind, key))
	}
	if key == "" {
		panic(fmt.Sprintf("register %s with empty key", kind))
	}

	h.registrationMu.Lock()
	defer h.registrationMu.Unlock()

	if h.registrations == nil {
		h.registrations = make(map[registrationKey]struct{})
	}
	registration := registrationKey{server: s, kind: kind, name: key}
	if _, exists := h.registrations[registration]; exists {
		panic(fmt.Sprintf("duplicate MCP %s registration: %q", kind, key))
	}
	h.registrations[registration] = struct{}{}
}

// HasRegisteredTool reports whether name was claimed through the checked
// registration path for this server. The runtime observer uses this state to
// keep unknown, attacker-controlled names out of telemetry dimensions.
func (h *Handler) HasRegisteredTool(s *mcp.Server, name string) bool {
	if h == nil || s == nil || name == "" {
		return false
	}
	h.registrationMu.RLock()
	defer h.registrationMu.RUnlock()
	_, ok := h.registrations[registrationKey{server: s, kind: registrationTool, name: name}]
	return ok
}

func (h *Handler) registerTool(s *mcp.Server, tool mcp.Tool, handler mcp.ToolHandlerFunc) {
	h.claimRegistration(s, registrationTool, tool.Name)
	s.AddTool(&tool, mcp.AdaptToolHandler(handler))
}

func (h *Handler) addResource(s *mcp.Server, resource mcp.Resource, handler mcp.ResourceHandlerFunc) {
	h.claimRegistration(s, registrationResource, resource.URI)
	s.AddResource(&resource, mcp.AdaptResourceHandler(handler))
}

func (h *Handler) addResourceTemplate(s *mcp.Server, resourceTemplate mcp.ResourceTemplate, handler mcp.ResourceTemplateHandlerFunc) {
	h.claimRegistration(s, registrationResourceTemplate, resourceTemplate.URITemplate)
	s.AddResourceTemplate(&resourceTemplate, mcp.AdaptResourceHandler(handler))
}

// RegisterPrompt exposes checked prompt registration to server composition
// package without letting prompt definitions bypass duplicate detection.
func (h *Handler) RegisterPrompt(s *mcp.Server, prompt mcp.Prompt, handler mcp.PromptHandlerFunc) {
	h.claimRegistration(s, registrationPrompt, prompt.Name)
	s.AddPrompt(&prompt, mcp.AdaptPromptHandler(handler))
}
