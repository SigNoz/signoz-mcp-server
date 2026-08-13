package mcpcontract

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type failingJSONSchema struct{}

func (failingJSONSchema) MarshalJSON() ([]byte, error) { return nil, fmt.Errorf("boom") }

func TestSchemaConversionFailuresIncludeDirectionAndTool(t *testing.T) {
	tests := []struct {
		name string
		run  func()
		want string
	}{
		{
			name: "marshal input schema",
			run: func() {
				tool := Tool{Name: "input_probe", InputSchema: failingJSONSchema{}}
				WithString("field")(&tool)
			},
			want: `marshal input schema for tool "input_probe" (type mcpcontract.failingJSONSchema):`,
		},
		{
			name: "decode input schema object",
			run: func() {
				tool := Tool{Name: "input_probe", InputSchema: json.RawMessage(`[]`)}
				WithString("field")(&tool)
			},
			want: `decode input schema for tool "input_probe" (type json.RawMessage) as object`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				got := fmt.Sprint(recover())
				if !strings.Contains(got, tt.want) {
					t.Fatalf("panic = %q, want context %q", got, tt.want)
				}
			}()
			tt.run()
		})
	}
}

func TestAdaptToolHandlerPreservesDecodedAndRawArguments(t *testing.T) {
	tests := []struct {
		name        string
		raw         json.RawMessage
		wantDecoded any
	}{
		{name: "omitted", raw: nil, wantDecoded: nil},
		{name: "null", raw: json.RawMessage(`null`), wantDecoded: nil},
		{name: "object and float64", raw: json.RawMessage(`{"n":9007199254740993,"s":"7"}`), wantDecoded: map[string]any{"n": float64(9007199254740992), "s": "7"}},
		{name: "array", raw: json.RawMessage(`[1,"two"]`), wantDecoded: []any{float64(1), "two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got CallToolRequest
			handler := AdaptToolHandler(func(_ context.Context, req CallToolRequest) (*CallToolResult, error) {
				got = req
				return NewToolResultText("ok"), nil
			})
			_, err := handler(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "probe", Arguments: tt.raw}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Params.Arguments, tt.wantDecoded) {
				t.Fatalf("decoded = %#v, want %#v", got.Params.Arguments, tt.wantDecoded)
			}
			if string(got.Params.RawArguments) != string(tt.raw) {
				t.Fatalf("raw = %q, want exact %q", got.Params.RawArguments, tt.raw)
			}
		})
	}
}

func TestAdaptResourceHandlerPreservesTextAndBlob(t *testing.T) {
	handler := AdaptResourceHandler(func(_ context.Context, req ReadResourceRequest) ([]ResourceContents, error) {
		return []ResourceContents{
			{URI: req.Params.URI, MIMEType: "text/plain", Text: "hello"},
			{URI: req.Params.URI + "/blob", MIMEType: "application/octet-stream", Blob: []byte{0, 1, 2}},
		}, nil
	})
	result, err := handler(context.Background(), &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: "signoz://probe"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contents) != 2 || result.Contents[0].Text != "hello" || !reflect.DeepEqual(result.Contents[1].Blob, []byte{0, 1, 2}) {
		t.Fatalf("adapted contents = %#v", result.Contents)
	}
	b, err := json.Marshal(result.Contents[1])
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"uri":"signoz://probe/blob","mimeType":"application/octet-stream","blob":"AAEC"}` {
		t.Fatalf("blob wire JSON = %s", b)
	}
}
