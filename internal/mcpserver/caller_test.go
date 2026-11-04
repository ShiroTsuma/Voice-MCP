package mcpserver

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCallerPrefersFixedClientThenGatewayMeta(t *testing.T) {
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{clientMetaKey: "claude-code"}}}
	if got := caller(req, ""); got != "claude-code" {
		t.Fatalf("meta client = %q", got)
	}
	if got := caller(req, "codex"); got != "codex" {
		t.Fatalf("fixed client = %q", got)
	}
	if got := caller(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}, ""); got != "" {
		t.Fatalf("no client = %q", got)
	}
}
