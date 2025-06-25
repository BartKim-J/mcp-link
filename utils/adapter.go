package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func prefixRequired(isRequired bool, desc string) string {
	if isRequired && !strings.HasPrefix(desc, "[required]") {
		return "[required] " + desc
	}
	return desc
}

func isRequiredField(field string, requiredList []string) bool {
	for _, name := range requiredList {
		if name == field {
			return true
		}
	}
	return false
}

func sanitizeToolName(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, ".", "_")
	s = strings.ReplaceAll(s, "{", "")
	s = strings.ReplaceAll(s, "}", "")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "?", "")
	s = strings.ReplaceAll(s, "&", "and")
	s = strings.ReplaceAll(s, "=", "_eq_")
	s = strings.ReplaceAll(s, "%", "_pct_")
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	s = strings.TrimSuffix(s, "_")
	s = strings.TrimPrefix(s, "_")
	if s == "" {
		return "unnamed_tool"
	}
	return s
}

func NewToolHandler(method string, url string, extraHeaders map[string]string) func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		params := request.Params.Arguments
		pathParams := make(map[string]interface{})
		queryParams := make(map[string]interface{})
		bodyParams := make(map[string]interface{})

		if pathParamsMap, ok := params["pathNames"].(map[string]interface{}); ok {
			pathParams = pathParamsMap
		}
		if urlParamsMap, ok := params["searchParams"].(map[string]interface{}); ok {
			queryParams = urlParamsMap
		}
		if requestBodyMap, ok := params["requestBody"].(map[string]interface{}); ok {
			bodyParams = requestBodyMap
		}

		if len(pathParams) == 0 && len(queryParams) == 0 && len(bodyParams) == 0 {
			for paramName, paramValue := range params {
				placeholder := fmt.Sprintf("{%s}", paramName)
				if strings.Contains(url, placeholder) {
					pathParams[paramName] = paramValue
				} else {
					bodyParams[paramName] = paramValue
				}
			}
		}

		finalURL := url
		for paramName, paramValue := range pathParams {
			placeholder := fmt.Sprintf("{%s}", paramName)
			if strings.Contains(finalURL, placeholder) {
				var strValue string
				switch v := paramValue.(type) {
				case string:
					strValue = v
				case nil:
					strValue = ""
				default:
					strValue = fmt.Sprintf("%v", v)
				}
				finalURL = strings.ReplaceAll(finalURL, placeholder, strValue)
			}
		}

		if len(queryParams) > 0 {
			parsedURL, err := neturl.Parse(finalURL)
			if err != nil {
				return mcp.NewToolResultText(fmt.Sprintf("Error parsing URL: %v", err)), nil
			}
			q := parsedURL.Query()
			for paramName, paramValue := range queryParams {
				var strValue string
				switch v := paramValue.(type) {
				case string:
					strValue = v
				case nil:
					continue
				default:
					strValue = fmt.Sprintf("%v", v)
				}
				q.Add(paramName, strValue)
			}
			parsedURL.RawQuery = q.Encode()
			finalURL = parsedURL.String()
		}

		var reqBody io.Reader = nil
		if len(bodyParams) > 0 {
			jsonParams, err := json.Marshal(bodyParams)
			if err != nil {
				return mcp.NewToolResultText(fmt.Sprintf("Error marshaling body parameters: %v", err)), nil
			}
			reqBody = bytes.NewBuffer(jsonParams)
		}

		req, err := http.NewRequestWithContext(ctx, method, finalURL, reqBody)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error creating request: %v", err)), nil
		}

		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for key, value := range extraHeaders {
			req.Header.Set(key, value)
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error executing request: %v", err)), nil
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return mcp.NewToolResultText(fmt.Sprintf("Error reading response: %v", err)), nil
		}

		return mcp.NewToolResultText(string(body)), nil
	}
}


func NewMCPFromCustomParser(baseURL string, extraHeaders map[string]string, parser OpenAPIParser) (*server.MCPServer, error) {
	apiInfo := parser.Info()
	prefix := sanitizeToolName(apiInfo.Title)

	s := server.NewMCPServer(
		prefix,
		apiInfo.Version,
		server.WithResourceCapabilities(true, true),
		server.WithLogging(),
	)

	for _, api := range parser.APIs() {
		name := sanitizeToolName(fmt.Sprintf("%s_%s", prefix, api.OperationID))
		opts := []mcp.ToolOption{
			mcp.WithDescription(api.OperationID + " " + api.Summary + " " + api.Description),
		}

		queryProps := map[string]interface{}{}
		requiredQueryParams := []string{}

		pathProps := map[string]interface{}{}
		requiredPathParams := []string{}

		for _, param := range api.Parameters {
			prop := map[string]interface{}{
				"type":        param.Schema.Type,
				"description": prefixRequired(param.Required, param.Description),
			}
			if param.Schema.Enum != nil {
				prop["enum"] = param.Schema.Enum
			}
			if param.Schema.Format != "" {
				prop["format"] = param.Schema.Format
			}
			if param.Schema.Default != nil {
				prop["default"] = param.Schema.Default
			}
			if param.Schema.Items != nil {
				prop["items"] = param.Schema.Items
			}
			if param.Schema.Properties != nil {
				prop["properties"] = param.Schema.Properties
			}

			switch param.In {
			case "query":
				queryProps[param.Name] = prop
				if param.Required {
					requiredQueryParams = append(requiredQueryParams, param.Name)
				}
			case "path":
				pathProps[param.Name] = prop
				if param.Required {
					requiredPathParams = append(requiredPathParams, param.Name)
				}
			}
		}

		if len(queryProps) > 0 {
			opts = append(opts, mcp.WithObject("searchParams",
				mcp.Description("url parameters for the tool"),
				mcp.Properties(queryProps),
				func(schema map[string]interface{}) {
					schema["required"] = requiredQueryParams
				},
			))
		}
		if len(pathProps) > 0 {
			opts = append(opts, mcp.WithObject("pathNames",
				mcp.Description("path parameters for the tool"),
				mcp.Properties(pathProps),
				func(schema map[string]interface{}) {
					schema["required"] = requiredPathParams
				},
			))
		}

		bodyProps := map[string]interface{}{}
		requiredBodyParams := []string{}

		if api.RequestBody != nil && len(api.RequestBody.Content) > 0 {
			for _, mediaType := range api.RequestBody.Content {
				if mediaType.Schema != nil {
					for propName, propSchema := range mediaType.Schema.Properties {
						prop := map[string]interface{}{
							"type":        propSchema.Type,
							"description": prefixRequired(isRequiredField(propName, mediaType.Schema.Required), propSchema.Description),
						}
						if propSchema.Enum != nil {
							prop["enum"] = propSchema.Enum
						}
						if propSchema.Format != "" {
							prop["format"] = propSchema.Format
						}
						if propSchema.Default != nil {
							prop["default"] = propSchema.Default
						}
						if propSchema.Items != nil {
							prop["items"] = propSchema.Items
						}
						if propSchema.Properties != nil {
							prop["properties"] = propSchema.Properties
						}
						bodyProps[propName] = prop
						if isRequiredField(propName, mediaType.Schema.Required) {
							requiredBodyParams = append(requiredBodyParams, propName)
						}
					}
				}
			}
			opts = append(opts, mcp.WithObject("requestBody",
				mcp.Description("request body for the tool"),
				mcp.Properties(bodyProps),
				func(schema map[string]interface{}) {
					schema["required"] = requiredBodyParams
				},
			))
		}

		tool := mcp.NewTool(name, opts...)
		handler := NewToolHandler(api.Method, baseURL+api.Path, extraHeaders)
		s.AddTool(tool, handler)
	}

	return s, nil
}
