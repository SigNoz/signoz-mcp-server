package tools

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type registrationKind string

type registrationKey struct {
	server *server.MCPServer
	kind   registrationKind
	name   string
}

const (
	registrationTool             registrationKind = "tool"
	registrationResource         registrationKind = "resource"
	registrationResourceTemplate registrationKind = "resource template"
	registrationPrompt           registrationKind = "prompt"
)

func (h *Handler) claimRegistration(s *server.MCPServer, kind registrationKind, key string) {
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

func (h *Handler) registerTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	h.claimRegistration(s, registrationTool, tool.Name)
	s.AddTool(tool, handler)
}

func (h *Handler) addResource(s *server.MCPServer, resource mcp.Resource, handler server.ResourceHandlerFunc) {
	h.claimRegistration(s, registrationResource, resource.URI)
	s.AddResource(resource, handler)
}

func (h *Handler) addResourceTemplate(s *server.MCPServer, resourceTemplate mcp.ResourceTemplate, handler server.ResourceTemplateHandlerFunc) {
	key := ""
	if resourceTemplate.URITemplate != nil && resourceTemplate.URITemplate.Template != nil {
		key = resourceTemplate.URITemplate.Raw()
	}
	h.claimRegistration(s, registrationResourceTemplate, key)
	s.AddResourceTemplate(resourceTemplate, handler)
}

// RegisterPrompt exposes checked prompt registration to server composition
// package without letting prompt definitions bypass duplicate detection.
func (h *Handler) RegisterPrompt(s *server.MCPServer, prompt mcp.Prompt, handler server.PromptHandlerFunc) {
	h.claimRegistration(s, registrationPrompt, prompt.Name)
	s.AddPrompt(prompt, handler)
}
